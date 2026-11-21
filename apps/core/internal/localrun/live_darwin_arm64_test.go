//go:build cgo

package localrun

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLiveMicrosandboxRun(t *testing.T) {
	if os.Getenv("SOUS_TEST_MICROSANDBOX") != "1" {
		t.Skip("requires verified runtime and hardware virtualization")
	}
	home, err := os.MkdirTemp("/private/tmp", "s-lr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	work := fixture(t)
	p := filepath.Join(work, "project")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(work, "host.txt"), "host only")
	put(t, filepath.Join(p, "note.txt"), "baseline")
	put(t, filepath.Join(p, ".env"), "not authorized")
	put(t, filepath.Join(p, "remove.txt"), "old")
	args := Options{Source: p, Output: work, GuestOptions: GuestOptions{Home: home, RuntimePath: os.Getenv("MSB_PATH"), FirmwarePath: os.Getenv("MSB_LIBKRUNFW_PATH"), Timeout: 30 * time.Second, Command: []string{"/bin/sh", "-ec", `test ! -e "$1"; test ! -e .env; test "$(cat note.txt)" = baseline; if wget -T 2 -q -O /dev/null https://example.com; then exit 42; fi; printf 'updated' > note.txt; rm remove.txt; mkdir sub; printf 'addition' > sub/add.txt; printf 'done\n'`, "test", filepath.Join(work, "host.txt")}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args.Offline = true
	if _, err := Run(ctx, args); err == nil || !strings.Contains(err.Error(), "离线模式未找到固定镜像") {
		t.Fatalf("cold offline run must fail closed: %v", err)
	}
	args.Offline = false
	args.ImageArchive = os.Getenv("SOUS_TEST_MSBOX_IMAGE_ARCHIVE")
	at := time.Now()
	result, err := Run(ctx, args)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("real Microsandbox run: %+v %v", result, err)
	}
	t.Logf("real import, exec, result and removal: %s", time.Since(at))
	if _, err := os.Stat(journal(home)); !os.IsNotExist(err) {
		t.Fatalf("active journal should be cleared after confirmed destroy: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(p, "note.txt")); err != nil || string(data) != "baseline" {
		t.Fatalf("host original changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(p, "remove.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p, "sub")); !os.IsNotExist(err) {
		t.Fatal("guest wrote to host source")
	}
	var report Report
	data, err := os.ReadFile(filepath.Join(result.Directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Imported != 2 || report.Skipped != 1 || len(report.Changes) != 3 {
		t.Fatalf("invalid review result: %+v", report)
	}
	for i, want := range []string{"modified", "removed", "added"} {
		if report.Changes[i].Operation != want {
			t.Fatalf("invalid review result: %+v", report)
		}
	}
	args.Source = filepath.Join(p, "note.txt")
	args.ImageArchive = ""
	args.Offline = true
	args.Command = []string{"/bin/sh", "-ec", `ln -s /etc/passwd unexpected-link`}
	if _, err := Run(ctx, args); err == nil {
		t.Fatal("accepted Guest link in artifact listing")
	}
	if _, err := os.Stat(journal(home)); !os.IsNotExist(err) {
		t.Fatalf("failed output left guest journal: %v", err)
	}
	args.Command = []string{"/bin/sleep", "60"}
	args.Timeout = time.Second
	if _, err := Run(ctx, args); err == nil {
		t.Fatal("Guest sleep exceeded timeout and reported success")
	}
	if _, err := os.Stat(journal(home)); !os.IsNotExist(err) {
		t.Fatalf("timed out run left guest journal: %v", err)
	}
}

// TestLiveCrashRecovery exercises an actual CLI kill, not a simulated error
// return. The command's progress event arrives only after a Guest Exec starts.
func TestLiveCrashRecovery(t *testing.T) {
	if os.Getenv("SOUS_TEST_MICROSANDBOX") != "1" || os.Getenv("SOUS_TEST_SANDBOX_BIN") == "" {
		t.Skip("requires fixed runtime and a freshly built sous-sandbox executable")
	}
	root, err := os.MkdirTemp("/private/tmp", "s-lr-crash-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	p, output, home := filepath.Join(root, "project"), filepath.Join(root, "results"), filepath.Join(root, "vm")
	for _, path := range []string{p, output} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(p, "note.txt"), "original")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := os.Getenv("SOUS_TEST_SANDBOX_BIN")
	base := []string{"--runtime-home", home, "--msb", os.Getenv("MSB_PATH"), "--firmware", os.Getenv("MSB_LIBKRUNFW_PATH")}
	recover := func() {
		cleanup, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		cmd := exec.CommandContext(cleanup, binary, append(slices.Clone(base), "--recover")...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("reconcile after interruption: %v %s", err, out)
		}
	}
	defer recover()
	start := slices.Clone(base)
	if archive := os.Getenv("SOUS_TEST_MSBOX_IMAGE_ARCHIVE"); archive != "" {
		start = append(start, "--image-archive", archive)
	}
	cmd := exec.CommandContext(ctx, binary, append(start, "--source", p, "--output", output, "--", "/bin/sleep", "90")...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	scan := bufio.NewScanner(stderr)
	begun := false
	var phases []string
	for scan.Scan() {
		phase := scan.Text()
		phases = append(phases, phase)
		if strings.Contains(phase, "正在 Guest 内运行命令") {
			begun = true
			break
		}
	}
	if !begun {
		t.Fatalf("Guest did not start: %v (%v)", phases, scan.Err())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed helper reported success")
	}
	if _, err := os.Stat(journal(home)); err != nil {
		t.Fatal("crash lost its recovery journal", err)
	}
	if entries, err := os.ReadDir(output); err != nil || len(entries) != 0 {
		t.Fatalf("unexpected export prior to recovery: %v %v", entries, err)
	}
	recover()
	if _, err := os.Stat(journal(home)); !os.IsNotExist(err) {
		t.Fatal("successful recovery did not clear journal", err)
	}
	next := exec.CommandContext(ctx, binary, append(slices.Clone(base), "--source", p, "--output", output, "--offline", "--", "/bin/sh", "-ec", `printf result > note.txt`)...)
	out, err := next.Output()
	if err != nil {
		t.Fatalf("new guest after recovery: %v", err)
	}
	var result Result
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.Directory, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(p, "note.txt")); err != nil || string(data) != "original" {
		t.Fatal("new guest changed original", err)
	}
}

// TestLiveCLIReviewApply takes the host copy all the way through an actual
// network-disabled Guest run and an explicit, separate CLI authorization. A real Git
// worktree makes it observable that .git (its link to an outside repository)
// never entered the Guest, while the expected tracked files did.
func TestLiveCLIReviewApply(t *testing.T) {
	if os.Getenv("SOUS_TEST_MICROSANDBOX") != "1" || os.Getenv("SOUS_TEST_SANDBOX_BIN") == "" {
		t.Skip("requires real runtime and built sandbox command")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/private/tmp", "s-lr-review-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	p, repo, output, home := filepath.Join(root, "project"), filepath.Join(root, "repo"), filepath.Join(root, "results"), filepath.Join(root, "vm")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	execute := func(bin, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		return cmd.Output()
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		g := append([]string{"-c", "user.name=Sous Fixture", "-c", "user.email=sous@example.test", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null"}, args...)
		out, err := execute("git", dir, g...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return string(out)
	}
	git(repo, "init", "-q")
	put(t, filepath.Join(repo, "note.txt"), "tracked baseline\n")
	put(t, filepath.Join(repo, "remove.txt"), "old file\n")
	put(t, filepath.Join(repo, "race.txt"), "original\n")
	git(repo, "add", "note.txt", "remove.txt", "race.txt")
	git(repo, "commit", "-qm", "baseline")
	git(repo, "worktree", "add", "-qd", p, "HEAD")
	put(t, filepath.Join(p, ".env"), "not authorized")
	binary := os.Getenv("SOUS_TEST_SANDBOX_BIN")
	cli := func(args ...string) []byte {
		t.Helper()
		out, err := execute(binary, root, args...)
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				t.Fatalf("sandbox %s: %v %s", args[0], err, e.Stderr)
			}
			t.Fatal(err)
		}
		return out
	}
	var result Result
	opts := []string{"--runtime-home", home, "--msb", os.Getenv("MSB_PATH"), "--firmware", os.Getenv("MSB_LIBKRUNFW_PATH"), "--source", p, "--output", output}
	if archive := os.Getenv("SOUS_TEST_MSBOX_IMAGE_ARCHIVE"); archive != "" {
		opts = append(opts, "--image-archive", archive)
	}
	initial := cli(append(opts, "--", "/bin/sh", "-ec", `test ! -e .git; test ! -e .env; printf 'Guest edit\n' > note.txt; rm remove.txt; mkdir -p docs/nested; printf 'new file\n' > docs/nested/guide.txt; printf 'guest idea\n' > race.txt`)...)
	if err := json.Unmarshal(initial, &result); err != nil {
		t.Fatal(err)
	}
	if git(p, "diff", "--name-only") != "" {
		t.Fatal("sandbox execution changed tracked host files before approval")
	}
	var review Review
	if err := json.Unmarshal(cli("review", result.Directory), &review); err != nil || !review.CanApply || len(review.Changes) != 4 {
		t.Fatalf("review: %+v %v", review, err)
	}
	put(t, filepath.Join(p, "race.txt"), "user editing\n")
	if _, err := execute(binary, root, "apply", result.Directory, review.Revision, "race.txt"); err == nil {
		t.Fatal("overwrote edited host file")
	}
	for _, target := range []string{"note.txt", "remove.txt", "docs/nested/guide.txt"} {
		cli("apply", result.Directory, review.Revision, target)
	}
	if got := git(p, "diff", "--name-status"); got != "M\tnote.txt\nM\trace.txt\nD\tremove.txt\n" {
		t.Fatalf("wrong tracked changes: %q", got)
	}
	if got := readText(t, filepath.Join(p, "docs/nested/guide.txt")); got != "new file\n" {
		t.Fatalf("wrong new file: %q", got)
	}
	if got := readText(t, filepath.Join(p, "race.txt")); got != "user editing\n" {
		t.Fatalf("did not preserve local edit: %q", got)
	}
	if got := readText(t, filepath.Join(repo, "note.txt")); got != "tracked baseline\n" {
		t.Fatalf("crossed into other Git worktree: %q", got)
	}
}

func TestLiveDesktopPipeCancelsAndReclaimsGuest(t *testing.T) {
	if os.Getenv("SOUS_TEST_MICROSANDBOX") != "1" || os.Getenv("SOUS_TEST_SANDBOX_BIN") == "" {
		t.Skip("requires supported runtime and built sandbox command")
	}
	root, err := os.MkdirTemp("/private/tmp", "s-lr-pipe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	p, output, home := filepath.Join(root, "project"), filepath.Join(root, "results"), filepath.Join(root, "vm")
	for _, name := range []string{p, output} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(p, "note.txt"), "original")
	id, err := IdentifySource(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := os.Getenv("SOUS_TEST_SANDBOX_BIN")
	base := []string{"--runtime-home", home, "--msb", os.Getenv("MSB_PATH"), "--firmware", os.Getenv("MSB_LIBKRUNFW_PATH")}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		cmd := exec.CommandContext(cleanup, binary, append(slices.Clone(base), "--recover")...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("clean up fixture VM: %v %s", err, out)
		}
	}()
	args := append(slices.Clone(base), "--source", p, "--source-identity", id, "--output", output, "--parent-managed")
	if archive := os.Getenv("SOUS_TEST_MSBOX_IMAGE_ARCHIVE"); archive != "" {
		args = append(args, "--image-archive", archive)
	}
	cmd := exec.CommandContext(ctx, binary, append(args, "--", "/bin/sleep", "90")...)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	scan := bufio.NewScanner(stderr)
	begun := false
	var log []string
	for scan.Scan() {
		line := scan.Text()
		log = append(log, line)
		if strings.Contains(line, "正在 Guest 内运行命令") {
			begun = true
			break
		}
	}
	if !begun {
		t.Fatalf("managed Guest did not start: %v", log)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := cmd.Wait(); err == nil || time.Since(started) > 20*time.Second {
		t.Fatalf("launcher did not promptly stop its Guest: %v (%s)", err, time.Since(started))
	}
	if _, err := os.Stat(journal(home)); !os.IsNotExist(err) {
		t.Fatal("Guest destruction is not confirmed", err)
	}
	if entries, err := os.ReadDir(output); err != nil || len(entries) != 0 {
		t.Fatalf("cancelled run exported a report: %v %v", entries, err)
	}
	if err := os.Rename(p, p+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(p, "note.txt"), "replacement")
	stale := exec.CommandContext(ctx, binary, append(slices.Clone(base), "--source", p, "--source-identity", id, "--output", output, "--offline", "--", "/bin/ls")...)
	out, err := stale.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "请在本机重新选择") {
		t.Fatalf("accepted stale desktop selection: %v %s", err, out)
	}
}

package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "my-repo")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	exec.Command("git", "-C", repo, "add", "-A").Run()
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "init").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	return repo
}

// prepareLocal prepares a task branch that starts at the checkout's current
// branch, as the supervisor would ask for "the current branch, locally".
func prepareLocal(ctx context.Context, t *testing.T, root, repo, key, branch, slug string) (Prepared, error) {
	t.Helper()
	cur := strings.TrimSpace(gitOut(repo, "rev-parse", "--abbrev-ref", "HEAD"))
	return PrepareTaskOn(ctx, root, repo, key, branch, slug, Start{Branch: cur, From: FromLocal})
}

func gitOut(dir string, args ...string) string {
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(out)
}

func TestWorktreeAndDiff(t *testing.T) {
	ctx := context.Background()
	repo := gitInit(t)
	root := t.TempDir()
	p, err := prepareLocal(ctx, t, root, filepath.Join(repo, "."), "01JABCDEFGHJKMNPQRST", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != "sous/"+ShortID("01JABCDEFGHJKMNPQRST") || !strings.HasPrefix(p.Dir, filepath.Join(root, "worktrees", "my-repo")) {
		t.Fatalf("prepared: %+v", p)
	}
	// The agent edits a file, adds one, and commits part of it.
	os.WriteFile(filepath.Join(p.Dir, "a.txt"), []byte("two\n"), 0o644)
	os.WriteFile(filepath.Join(p.Dir, "new.txt"), []byte("hi\n"), 0o644)
	exec.Command("git", "-C", p.Dir, "add", "a.txt").Run()
	exec.Command("git", "-C", p.Dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "edit").Run()

	diff, stat, err := Diff(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+two") || !strings.Contains(diff, "new.txt") || !strings.Contains(stat, "2 files changed") {
		t.Fatalf("diff:\n%s\nstat: %s", diff, stat)
	}
	// The user's checkout is untouched.
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "one\n" {
		t.Fatalf("main checkout changed: %q", b)
	}
	// Preparing the same run again reuses the worktree.
	again, err := prepareLocal(ctx, t, root, repo, "01JABCDEFGHJKMNPQRST", "", "")
	if err != nil || again.Dir != p.Dir {
		t.Fatalf("reuse: %v %+v", err, again)
	}
	if err := Remove(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Fatal("worktree still there")
	}
}

func TestPrepareTaskErrors(t *testing.T) {
	ctx := context.Background()
	st := Start{Branch: "main", From: FromLocal}
	if _, err := PrepareTaskOn(ctx, t.TempDir(), "relative/path", "r1", "", "", st); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := PrepareTaskOn(ctx, t.TempDir(), t.TempDir(), "r1", "", "", st); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("non-repo: %v", err)
	}
	// A new branch never starts from a branch sous picked by itself.
	repo := gitInit(t)
	for _, bad := range []Start{{}, {Branch: "main"}, {From: FromLocal}} {
		if _, err := PrepareTaskOn(ctx, t.TempDir(), repo, "r2", "", "", bad); err != ErrNoStart {
			t.Fatalf("start %+v: %v, want ErrNoStart", bad, err)
		}
	}
	if _, err := PrepareTaskOn(ctx, t.TempDir(), repo, "r3", "", "", Start{Branch: "nope", From: FromLocal}); err == nil || !strings.Contains(err.Error(), "没有本地分支 nope") {
		t.Fatalf("missing branch: %v", err)
	}
}

func TestGeneral(t *testing.T) {
	p, err := PrepareGeneral(t.TempDir(), "01JABCDEFGHJKMNPQRST")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		t.Fatal("scratch dir missing")
	}
	if d, s, err := Diff(context.Background(), p); err != nil || d != "" || s != "" {
		t.Fatalf("general diff: %q %q %v", d, s, err)
	}
}

// gitT runs git in dir and fails the test on error. It returns stdout only:
// git warns on stderr about line endings on Windows (autocrlf), which is not
// part of any answer.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// twoMachines returns a bare origin with one commit on main and a clone of
// it (this machine's checkout); other is another machine's clone.
func twoMachines(t *testing.T) (origin, mine, other string) {
	t.Helper()
	seed := gitInit(t)
	gitT(t, seed, "branch", "-M", "main")
	origin = filepath.Join(t.TempDir(), "origin.git")
	gitT(t, seed, "clone", "-q", "--bare", seed, origin)
	mine = filepath.Join(t.TempDir(), "mine")
	other = filepath.Join(t.TempDir(), "other")
	gitT(t, seed, "clone", "-q", origin, mine)
	gitT(t, seed, "clone", "-q", origin, other)
	return origin, mine, other
}

func commitFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", name)
	return gitT(t, dir, "rev-parse", "HEAD")
}

// A machine whose checkout is behind starts the task from the remote's
// latest code, and its own checkout is left alone.
func TestTaskStartsFromRemoteWhenBehind(t *testing.T) {
	ctx := context.Background()
	_, mine, other := twoMachines(t)
	before := gitT(t, mine, "rev-parse", "HEAD")
	latest := commitFile(t, other, "b.txt", "from the other machine\n")
	gitT(t, other, "push", "-q", "origin", "main")

	p, err := PrepareTaskOn(ctx, t.TempDir(), mine, "01JTASKREMOTEAAAAAAAAAAAAA", "", "x", Start{Branch: "main", From: FromRemote})
	if err != nil {
		t.Fatal(err)
	}
	if p.Base != latest || p.BaseBranch != "main" {
		t.Fatalf("base %s %q, want %s main", p.Base, p.BaseBranch, latest)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "b.txt")); err != nil {
		t.Fatal("the worktree lacks the other machine's commit")
	}
	if !strings.Contains(p.Note, "origin/main") {
		t.Errorf("note %q", p.Note)
	}
	if gitT(t, mine, "rev-parse", "HEAD") != before {
		t.Error("the user's checkout moved")
	}
	if up, _ := exec.Command("git", "-C", p.Dir, "rev-parse", "--abbrev-ref", "@{u}").Output(); len(up) > 0 {
		t.Errorf("the task branch tracks %s", up)
	}
	// Diffs are against where the task started, not the old local HEAD.
	os.WriteFile(filepath.Join(p.Dir, "c.txt"), []byte("task\n"), 0o644)
	diff, _, err := Diff(ctx, p)
	if err != nil || strings.Contains(diff, "b.txt") || !strings.Contains(diff, "c.txt") {
		t.Fatalf("diff %v %q", err, diff)
	}
}

// Starting from the remote leaves out local commits that were never pushed
// (the note says so); starting from the local branch includes them.
func TestTaskStartLocalOrRemote(t *testing.T) {
	_, mine, _ := twoMachines(t)
	local := commitFile(t, mine, "local.txt", "only here\n")
	p, err := PrepareTaskOn(context.Background(), t.TempDir(), mine, "01JTASKUNPUSHEDAAAAAAAAAAA", "", "", Start{Branch: "main", From: FromRemote})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Note, "1 个提交还没推送") {
		t.Fatalf("note %q", p.Note)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "local.txt")); err == nil {
		t.Error("an unpushed commit is in the remote-based task")
	}
	q, err := PrepareTaskOn(context.Background(), t.TempDir(), mine, "01JTASKLOCALSTARTBBBBBBBBB", "", "", Start{Branch: "main", From: FromLocal})
	if err != nil {
		t.Fatal(err)
	}
	if q.Base != local || q.BaseBranch != "main" || !strings.Contains(q.Note, "含 1 个还没推送的提交") {
		t.Fatalf("local start %+v", q)
	}
	if _, err := os.Stat(filepath.Join(q.Dir, "local.txt")); err != nil {
		t.Error("the local-based task lacks the unpushed commit")
	}
}

// A task that names its base branch starts from origin/<that branch>.
func TestTaskStartsFromRequestedBranch(t *testing.T) {
	_, mine, other := twoMachines(t)
	gitT(t, other, "checkout", "-qb", "release")
	rel := commitFile(t, other, "r.txt", "release\n")
	gitT(t, other, "push", "-q", "origin", "release")
	p, err := PrepareTaskOn(context.Background(), t.TempDir(), mine, "01JTASKRELEASEAAAAAAAAAAAA", "", "", Start{Branch: "release", From: FromRemote})
	if err != nil {
		t.Fatal(err)
	}
	if p.Base != rel || p.BaseBranch != "release" {
		t.Fatalf("base %s %q", p.Base, p.BaseBranch)
	}
}

// Without a reachable remote a remote start fails instead of quietly
// falling back to something else; a local start does not need the remote.
func TestRemoteStartNeedsTheRemote(t *testing.T) {
	ctx := context.Background()
	_, mine, _ := twoMachines(t)
	gitT(t, mine, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if _, err := PrepareTaskOn(ctx, t.TempDir(), mine, "01JTASKOFFLINEAAAAAAAAAAAA", "", "", Start{Branch: "main", From: FromRemote}); err == nil || !strings.Contains(err.Error(), "拉取 origin 失败") {
		t.Fatalf("offline remote start: %v", err)
	}
	head := gitT(t, mine, "rev-parse", "HEAD")
	p, err := PrepareTaskOn(ctx, t.TempDir(), mine, "01JTASKOFFLINEBBBBBBBBBBBB", "", "", Start{Branch: "main", From: FromLocal})
	if err != nil || p.Base != head {
		t.Fatalf("%+v %v", p, err)
	}
	repo := gitInit(t)
	if _, err := PrepareTaskOn(ctx, t.TempDir(), repo, "01JTASKLOCALONLYAAAAAAAAAA", "", "", Start{Branch: "master", From: FromRemote}); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("no origin: %v", err)
	}
}

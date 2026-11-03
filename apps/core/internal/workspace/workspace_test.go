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

func TestWorktreeAndDiff(t *testing.T) {
	ctx := context.Background()
	repo := gitInit(t)
	root := t.TempDir()
	p, err := PrepareCode(ctx, root, filepath.Join(repo, "."), "01JABCDEFGHJKMNPQRST")
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
	again, err := PrepareCode(ctx, root, repo, "01JABCDEFGHJKMNPQRST")
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

func TestPrepareCodeErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := PrepareCode(ctx, t.TempDir(), "relative/path", "r1"); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := PrepareCode(ctx, t.TempDir(), t.TempDir(), "r1"); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("non-repo: %v", err)
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

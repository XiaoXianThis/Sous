package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTaskWorktreeIsReused(t *testing.T) {
	ctx := context.Background()
	repo := gitInit(t)
	root := t.TempDir()
	p, err := prepareLocal(ctx, t, root, repo, "01JTASKAAAAAAAAAAAAAAAAAAA", "", Slug("Add dark mode 黑暗模式!"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Branch, "sous/add-dark-mode-") || p.BaseBranch == "" || p.Base == "" {
		t.Fatalf("prepared: %+v", p)
	}
	os.WriteFile(filepath.Join(p.Dir, "b.txt"), []byte("b\n"), 0o644)

	// A second run of the same task sees the work of the first.
	again, err := prepareLocal(ctx, t, root, repo, "01JTASKAAAAAAAAAAAAAAAAAAA", p.Branch, "")
	if err != nil || again.Dir != p.Dir || again.Branch != p.Branch || again.Base != p.Base || again.BaseBranch != p.BaseBranch {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	if _, err := Commit(ctx, again, "add b"); err != nil {
		t.Fatal(err)
	}

	// After the worktree is removed, the branch survives and comes back.
	if err := RemoveTask(ctx, root, repo, "01JTASKAAAAAAAAAAAAAAAAAAA"); err != nil {
		t.Fatal(err)
	}
	back, err := prepareLocal(ctx, t, root, repo, "01JTASKAAAAAAAAAAAAAAAAAAA", p.Branch, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(back.Dir, "b.txt")); err != nil || back.Base != p.Base {
		t.Fatalf("restored worktree lost the commit: %v %+v", err, back)
	}
	if diff, _, _ := Diff(ctx, back); !strings.Contains(diff, "b.txt") {
		t.Fatalf("diff against the task base: %s", diff)
	}
}

func TestDeliverPushesAndOpensPR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	ctx := context.Background()
	repo := gitInit(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	exec.Command("git", "init", "-q", "--bare", origin).Run()
	exec.Command("git", "-C", repo, "remote", "add", "origin", origin).Run()

	bin := t.TempDir()
	log := filepath.Join(bin, "gh.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"case \"$2\" in view) echo '{\"url\":\"https://github.com/u/r/pull/7\",\"state\":\"OPEN\",\"mergeable\":\"CONFLICTING\"}'; [ -f " + log + ".made ] || exit 1;;\n" +
		"create) touch " + log + ".made; echo https://github.com/u/r/pull/7;; esac\n"
	os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	p, err := prepareLocal(ctx, t, t.TempDir(), repo, "01JTASKBBBBBBBBBBBBBBBBBBB", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(p.Dir, "c.txt"), []byte("c\n"), 0o644)
	if made, err := Commit(ctx, p, "add c"); err != nil || !made {
		t.Fatalf("commit: %v %v", made, err)
	}
	if !strings.Contains(Commits(ctx, p), "add c") {
		t.Fatal("commit list")
	}
	if err := Push(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", origin, "branch").Output(); !strings.Contains(string(out), p.Branch) {
		t.Fatalf("branch not on origin: %s", out)
	}
	url, conflict, err := PullRequest(ctx, p, "Add c", "body")
	if err != nil || url != "https://github.com/u/r/pull/7" || !conflict {
		t.Fatalf("pr: %q %v %v", url, conflict, err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "pr create --head "+p.Branch) || !strings.Contains(string(calls), "--base") {
		t.Fatalf("gh calls:\n%s", calls)
	}
	if err := Push(ctx, p, true); err != nil {
		t.Fatalf("force push with lease: %v", err)
	}
}

func TestPushNeedsOrigin(t *testing.T) {
	ctx := context.Background()
	p, _ := prepareLocal(ctx, t, t.TempDir(), gitInit(t), "01JTASKCCCCCCCCCCCCCCCCCCC", "", "")
	if err := Push(ctx, p, false); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("want an origin error, got %v", err)
	}
}

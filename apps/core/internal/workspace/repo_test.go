package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateAndSync(t *testing.T) {
	ctx := context.Background()
	origin, mine, other := twoMachines(t)
	_ = origin
	latest := commitFile(t, other, "b.txt", "two\n")
	gitT(t, other, "push", "-q", "origin", "main")

	// Not fetched yet: the checkout does not know it is behind.
	branch, st := State(ctx, mine)
	if branch != "main" || st.Upstream != "origin/main" || st.Behind != 0 || st.Changes != 0 {
		t.Fatalf("%s %+v", branch, st)
	}
	res, err := Sync(ctx, mine)
	if err != nil || res.Outcome != "fast_forwarded" || gitT(t, mine, "rev-parse", "HEAD") != latest || res.Git.Behind != 0 || res.Git.FetchedAt == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if res, _ = Sync(ctx, mine); res.Outcome != "up_to_date" {
		t.Fatalf("%+v", res)
	}

	// Uncommitted changes: fetch only, nothing lost.
	commitFile(t, other, "c.txt", "three\n")
	gitT(t, other, "push", "-q", "origin", "main")
	os.WriteFile(filepath.Join(mine, "a.txt"), []byte("edited\n"), 0o644)
	os.WriteFile(filepath.Join(mine, "new.txt"), []byte("new\n"), 0o644)
	res, err = Sync(ctx, mine)
	if err != nil || res.Outcome != "dirty" || res.Git.Behind != 1 || res.Git.Changes != 1 || res.Git.Untracked != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(mine, "a.txt")); string(b) != "edited\n" {
		t.Fatal("the edit was lost")
	}

	// A local commit too: diverged, left alone.
	gitT(t, mine, "stash", "-u", "-q")
	commitFile(t, mine, "d.txt", "local\n")
	if res, _ = Sync(ctx, mine); res.Outcome != "diverged" || res.Git.Ahead != 1 || res.Git.Behind != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestWIPPushesWithoutTouchingTheCheckout(t *testing.T) {
	ctx := context.Background()
	origin, mine, _ := twoMachines(t)
	if res, err := WIP(ctx, mine, "Mac"); err != nil || res.Outcome != "nothing" {
		t.Fatalf("clean: %+v %v", res, err)
	}
	os.WriteFile(filepath.Join(mine, "a.txt"), []byte("edited\n"), 0o644)
	os.WriteFile(filepath.Join(mine, "new.txt"), []byte("new\n"), 0o644)
	os.WriteFile(filepath.Join(mine, ".gitignore"), []byte("secret.env\n"), 0o644)
	os.WriteFile(filepath.Join(mine, "secret.env"), []byte("KEY=1\n"), 0o644)
	gitT(t, mine, "add", "a.txt") // something staged, to see the index survive
	head := gitT(t, mine, "rev-parse", "HEAD")
	status := gitT(t, mine, "status", "--porcelain")

	res, err := WIP(ctx, mine, "My Mac!")
	if err != nil || res.Outcome != "pushed" || !strings.HasPrefix(res.Branch, "sous/wip/my-mac-") {
		t.Fatalf("%+v %v", res, err)
	}
	files := gitT(t, origin, "ls-tree", "-r", "--name-only", res.Branch)
	if !strings.Contains(files, "new.txt") || !strings.Contains(files, ".gitignore") || strings.Contains(files, "secret.env") {
		t.Fatalf("wip tree: %s", files)
	}
	if got := gitT(t, origin, "show", res.Branch+":a.txt"); got != "edited" {
		t.Fatalf("a.txt on the branch: %q", got)
	}
	if gitT(t, mine, "rev-parse", "HEAD") != head || gitT(t, mine, "status", "--porcelain") != status {
		t.Fatal("the checkout changed")
	}
}

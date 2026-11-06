package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// In a directory without git, an in-place task gets a snapshot in a shadow
// repository: the diff covers the whole task, the directory gets no .git,
// and Restore puts it back as it was.
func TestInPlaceSnapshotWithoutGit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("old\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "x", "big.js"), []byte("ignored\n"), 0o644)

	p, err := PrepareInPlaceTask(ctx, root, dir, "01JINPLACEAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if p.Base == "" || p.Shadow == "" || !strings.HasPrefix(p.Shadow, root) {
		t.Fatalf("prepared %+v", p)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("the project directory got a .git")
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("new\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "added.txt"), []byte("hi\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "node_modules", "x", "big.js"), []byte("changed\n"), 0o644)

	// A later run of the same task keeps the first snapshot.
	again, err := PrepareInPlaceTask(ctx, root, dir, "01JINPLACEAAAAAAAAAAAAAAAA")
	if err != nil || again.Base != p.Base {
		t.Fatalf("second run %v %+v", err, again)
	}
	diff, stat, err := Diff(ctx, again)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+new") || !strings.Contains(diff, "added.txt") || strings.Contains(diff, "node_modules") || !strings.Contains(stat, "2 files changed") {
		t.Fatalf("diff:\n%s\nstat %s", diff, stat)
	}

	changed, err := Restore(ctx, root, dir, p.Base)
	if err != nil || !strings.Contains(changed, "2 files changed") {
		t.Fatalf("restore %q %v", changed, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.txt")); string(b) != "old\n" {
		t.Fatalf("notes %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "added.txt")); !os.IsNotExist(err) {
		t.Fatal("a file the task added is still there")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "node_modules", "x", "big.js")); string(b) != "changed\n" {
		t.Fatal("restore touched an excluded directory")
	}
}

// In a git checkout the snapshot lives in the shadow repository too: the
// user's index, branch and untracked files are left alone.
func TestInPlaceSnapshotLeavesTheUsersRepoAlone(t *testing.T) {
	ctx := context.Background()
	repo := gitInit(t)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("staged by the user\n"), 0o644)
	gitT(t, repo, "add", "a.txt")
	staged := gitT(t, repo, "diff", "--cached", "--name-only")
	head := gitT(t, repo, "rev-parse", "HEAD")

	p, err := PrepareInPlaceTask(ctx, t.TempDir(), repo, "01JINPLACEGITAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "task.txt"), []byte("task\n"), 0o644)
	diff, _, err := Diff(ctx, p)
	if err != nil || !strings.Contains(diff, "task.txt") || strings.Contains(diff, "staged by the user") {
		t.Fatalf("diff %v\n%s", err, diff)
	}
	if gitT(t, repo, "diff", "--cached", "--name-only") != staged || gitT(t, repo, "rev-parse", "HEAD") != head {
		t.Fatal("the user's index or HEAD changed")
	}
}

// gitWithOrigin returns a checkout on main with a bare origin.
func gitWithOrigin(t *testing.T) (repo, origin string) {
	t.Helper()
	repo = gitInit(t)
	gitT(t, repo, "branch", "-M", "main")
	origin = filepath.Join(t.TempDir(), "origin.git")
	gitT(t, repo, "init", "-q", "--bare", origin)
	gitT(t, repo, "remote", "add", "origin", origin)
	gitT(t, repo, "push", "-q", "-u", "origin", "main")
	return repo, origin
}

func taskOn(ctx context.Context, t *testing.T, repo, key, branch string) Prepared {
	t.Helper()
	p, err := PrepareTaskOn(ctx, t.TempDir(), repo, key, "", "", Start{Branch: branch, From: FromLocal})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCommitTaskSquash(t *testing.T) {
	ctx := context.Background()
	repo, _ := gitWithOrigin(t)
	p := taskOn(ctx, t, repo, "01JCOMMITAAAAAAAAAAAAAAAAA", "main")
	commitFile(t, p.Dir, "one.txt", "1\n")
	commitFile(t, p.Dir, "two.txt", "2\n")
	os.WriteFile(filepath.Join(p.Dir, "three.txt"), []byte("3\n"), 0o644)
	res, err := CommitTask(ctx, p, "加三个文件", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commits != 1 || res.Before == res.After {
		t.Fatalf("%+v", res)
	}
	if msg := gitT(t, p.Dir, "log", "-1", "--format=%s"); msg != "加三个文件" {
		t.Fatalf("message %q", msg)
	}
	if out := gitT(t, p.Dir, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(out, "one.txt") || !strings.Contains(out, "three.txt") {
		t.Fatalf("files %s", out)
	}
}

// Merging into the branch the user has checked out moves it forward
// there, removes the task's worktree and branch, and undo brings both back.
func TestMergeIntoCheckedOutBranchAndUndo(t *testing.T) {
	ctx := context.Background()
	repo, _ := gitWithOrigin(t)
	root := t.TempDir()
	log := OpenGitLog(root)
	p := taskOn(ctx, t, repo, "01JMERGEAAAAAAAAAAAAAAAAAA", "main")
	os.WriteFile(filepath.Join(p.Dir, "feature.txt"), []byte("f\n"), 0o644)
	before := gitT(t, repo, "rev-parse", "main")

	res, err := MergeTask(ctx, p, "main", "加功能", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Before != before || res.After == before || res.Commits != 1 || res.TaskBranch != p.Branch {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatal("the user's checkout did not move forward")
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Fatal("the task worktree is still there")
	}
	if out := gitT(t, repo, "branch", "--list", p.Branch); out != "" {
		t.Fatal("the task branch is still there")
	}
	log.Add(GitLogEntry{Repo: repo, Op: "merge", Branch: "main", Before: res.Before, After: res.After, TaskBranch: res.TaskBranch})

	undo, err := log.Undo(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if gitT(t, repo, "rev-parse", "main") != before || gitT(t, repo, "rev-parse", p.Branch) != res.After {
		t.Fatalf("undo %+v", undo)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); !os.IsNotExist(err) {
		t.Fatal("the merged file is still in the checkout")
	}
	if _, err := log.Undo(ctx, repo, "main"); err == nil {
		t.Fatal("undid twice")
	}
}

// A branch that is not checked out anywhere moves with update-ref; the
// user's checkout (on another branch) does not change.
func TestMergeIntoBranchNotCheckedOut(t *testing.T) {
	ctx := context.Background()
	repo, _ := gitWithOrigin(t)
	gitT(t, repo, "branch", "dev")
	p := taskOn(ctx, t, repo, "01JMERGEDEVAAAAAAAAAAAAAAA", "dev")
	commitFile(t, p.Dir, "d.txt", "d\n")
	res, err := MergeTask(ctx, p, "dev", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if gitT(t, repo, "rev-parse", "dev") != res.After || gitT(t, repo, "rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Fatal("dev did not move, or the checkout changed branch")
	}
	if _, err := os.Stat(filepath.Join(repo, "d.txt")); err == nil {
		t.Fatal("the checkout on main got dev's file")
	}
}

func TestMergeRefusesConflictsAndDirtyCheckouts(t *testing.T) {
	ctx := context.Background()
	repo, _ := gitWithOrigin(t)
	p := taskOn(ctx, t, repo, "01JMERGECONFLICTAAAAAAAAAA", "main")
	commitFile(t, p.Dir, "a.txt", "task\n")
	commitFile(t, repo, "a.txt", "user\n")
	main := gitT(t, repo, "rev-parse", "main")
	tip := gitT(t, p.Dir, "rev-parse", "HEAD")

	res, err := MergeTask(ctx, p, "main", "", false)
	if !errors.Is(err, ErrConflict) || len(res.Conflicts) != 1 || res.Conflicts[0] != "a.txt" {
		t.Fatalf("%+v %v", res, err)
	}
	if gitT(t, repo, "rev-parse", "main") != main || gitT(t, p.Dir, "rev-parse", "HEAD") != tip {
		t.Fatal("a conflicting merge changed something")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".git")); err != nil {
		t.Fatal("the worktree is gone after a conflict")
	}

	q := taskOn(ctx, t, repo, "01JMERGEDIRTY0123456789XYZ", "main")
	commitFile(t, q.Dir, "q.txt", "q\n")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("uncommitted\n"), 0o644)
	if _, err := MergeTask(ctx, q, "main", "", false); err == nil || !strings.Contains(err.Error(), "未提交的改动") {
		t.Fatalf("dirty checkout: %v", err)
	}
	if _, err := MergeTask(ctx, q, "nope", "", false); err == nil || !strings.Contains(err.Error(), "没有本地分支 nope") {
		t.Fatalf("missing target: %v", err)
	}
}

func TestPushAndUndoAfterPush(t *testing.T) {
	ctx := context.Background()
	repo, origin := gitWithOrigin(t)
	log := OpenGitLog(t.TempDir())
	p := taskOn(ctx, t, repo, "01JPUSHAAAAAAAAAAAAAAAAAAA", "main")
	os.WriteFile(filepath.Join(p.Dir, "p.txt"), []byte("p\n"), 0o644)
	m, err := MergeTask(ctx, p, "main", "", false)
	if err != nil {
		t.Fatal(err)
	}
	log.Add(GitLogEntry{Repo: repo, Op: "merge", Branch: "main", Before: m.Before, After: m.After, TaskBranch: m.TaskBranch})

	remote, err := PushRemote(ctx, repo, "main", "")
	if err != nil || remote != "origin" {
		t.Fatalf("remote %q %v", remote, err)
	}
	res, err := PushBranch(ctx, repo, "main", remote)
	if err != nil || res.Commits != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if out, _ := exec.Command("git", "-C", origin, "rev-parse", "main").Output(); strings.TrimSpace(string(out)) != m.After {
		t.Fatal("origin did not get the commit")
	}
	if _, err := log.Undo(ctx, repo, "main"); err == nil || !strings.Contains(err.Error(), "推送") {
		t.Fatalf("undo after push: %v", err)
	}
	// Pushing again has nothing to do.
	if res, err := PushBranch(ctx, repo, "main", "origin"); err != nil || !strings.Contains(res.Message, "不用推送") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestPushRemoteDoesNotGuess(t *testing.T) {
	ctx := context.Background()
	repo := gitInit(t)
	gitT(t, repo, "branch", "-M", "main")
	if _, err := PushRemote(ctx, repo, "main", ""); err == nil {
		t.Fatal("no remote at all")
	}
	gitT(t, repo, "remote", "add", "a", t.TempDir())
	gitT(t, repo, "remote", "add", "b", t.TempDir())
	if _, err := PushRemote(ctx, repo, "main", ""); err == nil || !strings.Contains(err.Error(), "多个远端") {
		t.Fatalf("ambiguous: %v", err)
	}
	if r, err := PushRemote(ctx, repo, "main", "b"); err != nil || r != "b" {
		t.Fatalf("explicit %q %v", r, err)
	}
	if _, err := PushRemote(ctx, repo, "main", "c"); err == nil {
		t.Fatal("unknown remote accepted")
	}
}

func TestDiscardTaskAndStatus(t *testing.T) {
	ctx := context.Background()
	repo, _ := gitWithOrigin(t)
	p := taskOn(ctx, t, repo, "01JDISCARDAAAAAAAAAAAAAAAA", "main")
	commitFile(t, p.Dir, "x.txt", "x\n")
	commitFile(t, repo, "local.txt", "l\n") // main is one ahead of origin

	st, err := Status(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || st.Detached || st.Ahead != 1 || st.Upstream != "origin/main" || len(st.Remotes) != 1 {
		t.Fatalf("%+v", st)
	}
	var task *struct{ wt string }
	for _, b := range st.Branches {
		if b.Name == p.Branch {
			task = &struct{ wt string }{b.Worktree}
		}
	}
	if task == nil || task.wt == "" {
		t.Fatalf("task branch missing or not in its worktree: %+v", st.Branches)
	}

	res, err := DiscardTask(ctx, p)
	if err != nil || res.Before == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if out := gitT(t, repo, "branch", "--list", p.Branch); out != "" {
		t.Fatal("branch still there")
	}
	gitT(t, repo, "checkout", "-q", "--detach")
	if st, _ := Status(ctx, repo); !st.Detached || st.Branch != "" {
		t.Fatalf("detached %+v", st)
	}
}

// Committing an in-place task takes only the files the task changed: the
// user's own edits and staged files are left as they were.
func TestCommitInPlaceTakesOnlyTheTasksFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := gitInit(t)
	gitT(t, repo, "branch", "-M", "feature-x")
	os.WriteFile(filepath.Join(repo, "mine.txt"), []byte("the user's own work\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("staged by the user\n"), 0o644)
	gitT(t, repo, "add", "staged.txt")

	p, err := PrepareInPlaceTask(ctx, root, repo, "01JINPLACECOMMITAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hi\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("changed by the task\n"), 0o644)

	res, err := CommitInPlace(ctx, root, repo, p.Base, "加 hello")
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "feature-x" || res.Before == res.After || !strings.Contains(res.Message, "2 个文件") {
		t.Fatalf("%+v", res)
	}
	files := gitT(t, repo, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "hello.txt") || !strings.Contains(files, "a.txt") || strings.Contains(files, "mine.txt") || strings.Contains(files, "staged.txt") {
		t.Fatalf("committed files: %s", files)
	}
	if st := gitT(t, repo, "status", "--porcelain"); !strings.Contains(st, "A  staged.txt") || !strings.Contains(st, "?? mine.txt") {
		t.Fatalf("the user's changes moved: %s", st)
	}
	// Nothing left to commit the second time.
	again, err := CommitInPlace(ctx, root, repo, gitT(t, repo, "rev-parse", "HEAD"), "x")
	if err == nil && again.After != "" {
		t.Fatalf("committed again: %+v", again)
	}
}

// Package workspace prepares the directory a run works in: a git worktree on
// a new branch for code runs, so the user's own checkout is never touched,
// or a scratch directory for general runs. It also collects a run's diff.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Prepared is a run's working directory.
type Prepared struct {
	Dir    string
	Repo   string // the repository's top-level directory; empty for general runs
	Branch string
	Base   string // the commit the branch started from
	// BaseBranch is the branch HEAD was on when the task branch started:
	// the target of its pull request. Empty when HEAD was detached.
	BaseBranch string
}

const gitTimeout = 2 * time.Minute

// ShortID is the part of a run id used in directory and branch names.
func ShortID(runID string) string {
	id := strings.ToLower(runID)
	if len(id) > 10 {
		id = id[len(id)-10:]
	}
	return id
}

// PrepareCode creates a worktree of repoPath at root/worktrees/<repo>/<id> on
// branch sous/<id>. A second call for the same run reuses it.
func PrepareCode(ctx context.Context, root, repoPath, runID string) (Prepared, error) {
	return PrepareTask(ctx, root, repoPath, runID, "", "")
}

// PrepareTask creates or reuses the worktree of one task, keyed by key (the
// task id), at root/worktrees/<repo>/<short key>. Every run of a task works
// on the same branch: branch when given, else sous/<slug>-<short key>. The
// branch starts at the repository's HEAD; its base commit and base branch
// are remembered in the repository's git config for diffs and pull requests.
func PrepareTask(ctx context.Context, root, repoPath, key, branch, slug string) (Prepared, error) {
	if !filepath.IsAbs(repoPath) {
		return Prepared{}, fmt.Errorf("repo_path %q must be an absolute path", repoPath)
	}
	top, err := git(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return Prepared{}, fmt.Errorf("%s is not a git repository: %w", repoPath, err)
	}
	short := ShortID(key)
	if branch == "" {
		branch = "sous/" + short
		if slug != "" {
			branch = "sous/" + slug + "-" + short
		}
	}
	p := Prepared{Dir: filepath.Join(root, "worktrees", filepath.Base(top), short), Repo: top, Branch: branch}
	if _, err := os.Stat(p.Dir); err == nil {
		if cur, err := git(ctx, p.Dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			p.Branch = cur // a resumed run keeps its branch and what was done so far
			p.Base, p.BaseBranch = baseOf(ctx, top, cur)
			return p, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Dir), 0o755); err != nil {
		return Prepared{}, err
	}
	git(ctx, top, "worktree", "prune")
	if _, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		// The branch exists from an earlier run whose worktree is gone.
		if _, err := git(ctx, top, "worktree", "add", p.Dir, branch); err != nil {
			return Prepared{}, fmt.Errorf("create worktree: %w", err)
		}
		p.Base, p.BaseBranch = baseOf(ctx, top, branch)
		return p, nil
	}
	base, err := git(ctx, top, "rev-parse", "HEAD")
	if err != nil {
		// sous never commits in a user's repository on its own; say what to do.
		return Prepared{}, fmt.Errorf("%s 还没有任何提交，请先在该目录执行一次 git add -A 和 git commit", top)
	}
	baseBranch, _ := git(ctx, top, "rev-parse", "--abbrev-ref", "HEAD")
	if baseBranch == "HEAD" {
		baseBranch = "" // detached
	}
	if _, err := git(ctx, top, "worktree", "add", "-b", branch, p.Dir, base); err != nil {
		return Prepared{}, fmt.Errorf("create worktree: %w", err)
	}
	git(ctx, top, "config", "branch."+branch+".sousbase", base)
	if baseBranch != "" {
		git(ctx, top, "config", "branch."+branch+".sousbaseref", baseBranch)
	}
	p.Base, p.BaseBranch = base, baseBranch
	return p, nil
}

// baseOf reads where a task branch started.
func baseOf(ctx context.Context, top, branch string) (base, baseBranch string) {
	base, _ = git(ctx, top, "config", "branch."+branch+".sousbase")
	baseBranch, _ = git(ctx, top, "config", "branch."+branch+".sousbaseref")
	if base == "" {
		ref := baseBranch
		if ref == "" {
			ref = "HEAD"
		}
		base, _ = git(ctx, top, "merge-base", branch, ref)
	}
	return base, baseBranch
}

// RemoveTask deletes a task's worktree and keeps its branch.
func RemoveTask(ctx context.Context, root, repoPath, key string) error {
	top, err := git(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "worktrees", filepath.Base(top), ShortID(key))
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_, err = git(ctx, top, "worktree", "remove", "--force", dir)
	return err
}

// Slug turns a title into a short ASCII branch-name part, or "".
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 30 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// PrepareGeneral creates root/scratch/<id>.
func PrepareGeneral(root, runID string) (Prepared, error) {
	dir := filepath.Join(root, "scratch", ShortID(runID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Prepared{}, err
	}
	return Prepared{Dir: dir}, nil
}

// Diff returns every change in the worktree against the base commit,
// committed or not, with a short stat. General runs have no diff.
func Diff(ctx context.Context, p Prepared) (diff, stat string, err error) {
	if p.Base == "" {
		return "", "", nil
	}
	// Stage everything, including new files, so the diff sees them.
	if _, err := git(ctx, p.Dir, "add", "-A"); err != nil {
		return "", "", err
	}
	if diff, err = git(ctx, p.Dir, "diff", "--cached", "--binary", p.Base); err != nil {
		return "", "", err
	}
	if stat, err = git(ctx, p.Dir, "diff", "--cached", "--shortstat", p.Base); err != nil {
		return "", "", err
	}
	return diff, strings.TrimSpace(stat), nil
}

// Remove deletes a worktree (keeping its branch) or a scratch directory.
func Remove(ctx context.Context, p Prepared) error {
	if p.Repo != "" {
		_, err := git(ctx, p.Repo, "worktree", "remove", "--force", p.Dir)
		return err
	}
	return os.RemoveAll(p.Dir)
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

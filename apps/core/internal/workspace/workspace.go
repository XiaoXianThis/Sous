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
	if !filepath.IsAbs(repoPath) {
		return Prepared{}, fmt.Errorf("repo_path %q must be an absolute path", repoPath)
	}
	top, err := git(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return Prepared{}, fmt.Errorf("%s is not a git repository: %w", repoPath, err)
	}
	base, err := git(ctx, top, "rev-parse", "HEAD")
	if err != nil {
		return Prepared{}, fmt.Errorf("repository has no commits yet: %w", err)
	}
	short := ShortID(runID)
	p := Prepared{
		Dir:    filepath.Join(root, "worktrees", filepath.Base(top), short),
		Repo:   top,
		Branch: "sous/" + short,
		Base:   base,
	}
	if _, err := os.Stat(p.Dir); err == nil {
		if _, err := git(ctx, p.Dir, "rev-parse", "HEAD"); err == nil {
			return p, nil // a resumed run keeps its branch and what was done so far
		}
	}
	if err := os.MkdirAll(filepath.Dir(p.Dir), 0o755); err != nil {
		return Prepared{}, err
	}
	if _, err := git(ctx, top, "worktree", "add", "-b", p.Branch, p.Dir, base); err != nil {
		return Prepared{}, fmt.Errorf("create worktree: %w", err)
	}
	return p, nil
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

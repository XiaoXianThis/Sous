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
	// Note says where a new branch started, for the run's log: the remote's
	// latest code, or the local HEAD and why. Empty for a reused branch.
	Note string
	// Shadow is the shadow repository of an in-place task (its git dir; the
	// work tree is Dir). Base is then the snapshot taken before the task.
	Shadow string
}

// git runs git on the prepared directory: through the shadow repository
// for an in-place task, else in Dir itself.
func (p Prepared) git(ctx context.Context, args ...string) (string, error) {
	if p.Shadow != "" {
		return gitEnv(ctx, p.Dir, []string{"GIT_DIR=" + p.Shadow, "GIT_WORK_TREE=" + p.Dir}, args...)
	}
	return git(ctx, p.Dir, args...)
}

const (
	gitTimeout   = 2 * time.Minute
	fetchTimeout = 30 * time.Second
)

// ShortID is the part of a run id used in directory and branch names.
func ShortID(runID string) string {
	id := strings.ToLower(runID)
	if len(id) > 10 {
		id = id[len(id)-10:]
	}
	return id
}

// Start says where a new task branch starts (decision 0022). There is no
// default: Branch names the branch, From says whether the local branch
// (with commits not pushed yet) or origin/<Branch> after a fetch is meant.
type Start struct {
	Branch string
	From   string // "local" / "remote"
}

// Where a task branch starts from.
const (
	FromLocal  = "local"
	FromRemote = "remote"
)

// PrepareTaskOn creates or reuses the worktree of one task, keyed by key
// (the task id), at root/worktrees/<repo>/<short key>. Every run of a task
// works on the same branch: branch when given, else sous/<slug>-<short key>.
// A new branch starts where start says; its base commit and base branch are
// remembered in the repository's git config for diffs. The user's checkout
// is not touched.
func PrepareTaskOn(ctx context.Context, root, repoPath, key, branch, slug string, start Start) (Prepared, error) {
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
	base, note, err := startPoint(ctx, top, start)
	if err != nil {
		return Prepared{}, err
	}
	p.Note = note
	if _, err := git(ctx, top, "worktree", "add", "--no-track", "-b", branch, p.Dir, base); err != nil {
		return Prepared{}, fmt.Errorf("create worktree: %w", err)
	}
	git(ctx, top, "config", "branch."+branch+".sousbase", base)
	git(ctx, top, "config", "branch."+branch+".sousbaseref", start.Branch)
	p.Base, p.BaseBranch = base, start.Branch
	return p, nil
}

// ErrNoStart is returned when a new task branch has no explicit start.
var ErrNoStart = errors.New("没有指定任务分支从哪里开始：需要起点分支和来源（local 或 remote）")

// startPoint resolves the commit a new task branch starts from. It never
// picks a branch by itself.
func startPoint(ctx context.Context, top string, st Start) (base, note string, err error) {
	if st.Branch == "" || (st.From != FromLocal && st.From != FromRemote) {
		return "", "", ErrNoStart
	}
	if _, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		// sous never commits in a user's repository on its own; say what to do.
		return "", "", fmt.Errorf("%s 还没有任何提交，请先在该目录执行一次 git add -A 和 git commit", top)
	}
	if st.From == FromLocal {
		base, err = git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+st.Branch+"^{commit}")
		if err != nil || base == "" {
			return "", "", fmt.Errorf("这台机器上没有本地分支 %s", st.Branch)
		}
		note = fmt.Sprintf("基于本地的 %s（%s）开始", st.Branch, short(base))
		if up, err := git(ctx, top, "rev-parse", "--abbrev-ref", st.Branch+"@{upstream}"); err == nil && up != "" {
			if n, _ := git(ctx, top, "rev-list", "--count", up+".."+st.Branch); n != "" && n != "0" {
				note += fmt.Sprintf("，含 %s 个还没推送的提交", n)
			}
		}
		return base, note, nil
	}
	if _, err := git(ctx, top, "remote", "get-url", "origin"); err != nil {
		return "", "", errors.New("仓库没有名为 origin 的远端，不能基于远端分支开始")
	}
	if out, err := Fetch(ctx, top); err != nil {
		return "", "", fmt.Errorf("拉取 origin 失败（%s），没有基于远端开始", firstLine(out, err))
	}
	base, err = git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+st.Branch+"^{commit}")
	if err != nil || base == "" {
		return "", "", fmt.Errorf("origin 上没有分支 %s", st.Branch)
	}
	note = fmt.Sprintf("基于 origin/%s 的最新代码（%s）开始", st.Branch, short(base))
	if local, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+st.Branch); err == nil && local != base {
		if n, _ := git(ctx, top, "rev-list", "--count", "refs/remotes/origin/"+st.Branch+".."+st.Branch); n != "" && n != "0" {
			note += fmt.Sprintf("；这台机器本地的 %s 有 %s 个提交还没推送，任务里没有它们", st.Branch, n)
		}
	}
	return base, note, nil
}

// Fetch updates the remote-tracking branches of origin without asking for
// credentials: a prompt would hang the run.
func Fetch(ctx context.Context, top string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", top, "fetch", "--prune", "--quiet", "origin")
	cmd.Env = noPrompt()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("超过 %v 没有完成", fetchTimeout)
	}
	return string(out), err
}

// remoteDefault is the branch origin/HEAD points at ("main"), or "".
func remoteDefault(ctx context.Context, top string) string {
	ref, err := git(ctx, top, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(ref, "refs/remotes/origin/")
}

func firstLine(out string, err error) string {
	s := strings.TrimSpace(out)
	if s == "" && err != nil {
		s = err.Error()
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func orHead(branch string) string {
	if branch == "" {
		return "HEAD"
	}
	return branch
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
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
	// Stage everything, including new files, so the diff sees them. For an
	// in-place task this is the shadow repository's index, not the user's.
	if _, err := p.git(ctx, "add", "-A"); err != nil {
		return "", "", err
	}
	if diff, err = p.git(ctx, "diff", "--cached", "--binary", p.Base); err != nil {
		return "", "", err
	}
	if stat, err = p.git(ctx, "diff", "--cached", "--shortstat", p.Base); err != nil {
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

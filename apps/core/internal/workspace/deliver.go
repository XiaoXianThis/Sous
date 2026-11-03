package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Commit stages everything in the worktree and commits it, if anything is
// left uncommitted. It reports whether it made a commit.
func Commit(ctx context.Context, p Prepared, message string) (bool, error) {
	if _, err := git(ctx, p.Dir, "add", "-A"); err != nil {
		return false, err
	}
	if out, _ := git(ctx, p.Dir, "diff", "--cached", "--name-only"); strings.TrimSpace(out) == "" {
		return false, nil
	}
	_, err := git(ctx, p.Dir, "-c", "user.name="+authorName(ctx, p), "-c", "user.email="+authorEmail(ctx, p),
		"commit", "-q", "-m", message)
	return err == nil, err
}

// authorName and authorEmail fall back to a sous identity when the
// repository has none configured, so a commit never fails for that reason.
func authorName(ctx context.Context, p Prepared) string {
	if v, err := git(ctx, p.Dir, "config", "user.name"); err == nil && v != "" {
		return v
	}
	return "sous"
}

func authorEmail(ctx context.Context, p Prepared) string {
	if v, err := git(ctx, p.Dir, "config", "user.email"); err == nil && v != "" {
		return v
	}
	return "sous@localhost"
}

// Commits lists the task branch's commits since its base, one line each.
func Commits(ctx context.Context, p Prepared) string {
	if p.Base == "" {
		return ""
	}
	out, _ := git(ctx, p.Dir, "log", "--oneline", "--no-decorate", p.Base+"..HEAD")
	return out
}

// Push pushes the task branch to origin. After a rebase, force pushes with
// a lease, so it never overwrites work it has not seen.
func Push(ctx context.Context, p Prepared, force bool) error {
	if _, err := git(ctx, p.Dir, "remote", "get-url", "origin"); err != nil {
		return errors.New("仓库没有名为 origin 的远端，无法 push")
	}
	args := []string{"push", "-u", "origin", p.Branch}
	if force {
		args = []string{"push", "--force-with-lease", "-u", "origin", p.Branch}
	}
	_, err := git(ctx, p.Dir, args...)
	return err
}

// PullRequest opens a pull request for the task branch with the gh CLI, or
// finds the one already open. It reports whether the pull request conflicts
// with its base branch. sous never merges it.
func PullRequest(ctx context.Context, p Prepared, title, body string) (url string, conflict bool, err error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", false, errors.New("找不到 gh 命令：分支已 push，但没法创建 PR。安装并登录 GitHub CLI（gh auth login）后重试")
	}
	type view struct {
		URL       string `json:"url"`
		State     string `json:"state"`
		Mergeable string `json:"mergeable"`
	}
	look := func() (view, error) {
		var v view
		out, err := run(ctx, p.Dir, "gh", "pr", "view", p.Branch, "--json", "url,state,mergeable")
		if err != nil {
			return v, err
		}
		return v, json.Unmarshal([]byte(out), &v)
	}
	v, err := look()
	if err != nil || v.State != "OPEN" {
		args := []string{"pr", "create", "--head", p.Branch, "--title", title, "--body", body}
		if p.BaseBranch != "" {
			args = append(args, "--base", p.BaseBranch)
		}
		out, err := run(ctx, p.Dir, "gh", args...)
		if err != nil {
			return "", false, fmt.Errorf("创建 PR 失败：%w", err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		v = view{URL: strings.TrimSpace(lines[len(lines)-1]), Mergeable: "UNKNOWN"}
	}
	// GitHub computes mergeability in the background; wait a little for it.
	for i := 0; i < 5 && v.Mergeable == "UNKNOWN"; i++ {
		select {
		case <-ctx.Done():
			return v.URL, false, nil
		case <-time.After(2 * time.Second):
		}
		if nv, err := look(); err == nil {
			v.Mergeable = nv.Mergeable
			if nv.URL != "" {
				v.URL = nv.URL
			}
		}
	}
	return v.URL, v.Mergeable == "CONFLICTING", nil
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

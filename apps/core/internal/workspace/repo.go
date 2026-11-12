package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Keeping a project's checkouts on several machines in step
// (work/specs/multi-machine-sync): bring one up to date with its upstream
// without ever merging or losing work, and save uncommitted work to a
// branch on the remote without touching the checkout.

// Sync fetches and fast-forwards the checkout's branch to its upstream.
// It does nothing more when the checkout has uncommitted changes, has
// commits of its own (diverged) or tracks no upstream: it says why.
func Sync(ctx context.Context, dir string) (proto.RepoResult, error) {
	if out, err := Fetch(ctx, dir); err != nil {
		return proto.RepoResult{}, fmt.Errorf("拉取失败：%s", firstLine(out, err))
	}
	branch, st := State(ctx, dir)
	if st == nil {
		return proto.RepoResult{}, fmt.Errorf("%s 不是 git 仓库", dir)
	}
	res := proto.RepoResult{Git: st}
	switch {
	case st.Upstream == "":
		res.Outcome, res.Message = "no_upstream", fmt.Sprintf("已拉取；分支 %s 没有跟踪远端分支，没有更新", orHead(branch))
	case st.Behind == 0:
		res.Outcome, res.Message = "up_to_date", fmt.Sprintf("已是最新（%s）", st.Upstream)
	case st.Changes > 0:
		res.Outcome, res.Message = "dirty", fmt.Sprintf("落后 %s %d 个提交，但有 %d 个文件没提交，没有更新：先提交或存成 WIP 分支", st.Upstream, st.Behind, st.Changes)
	case st.Ahead > 0:
		res.Outcome, res.Message = "diverged", fmt.Sprintf("和 %s 分叉了（本地多 %d 个、远端多 %d 个提交），需要你自己合并或 rebase", st.Upstream, st.Ahead, st.Behind)
	default:
		n := st.Behind
		if _, err := git(ctx, dir, "merge", "--ff-only", "--quiet", "@{u}"); err != nil {
			return proto.RepoResult{}, fmt.Errorf("快进失败：%v", err)
		}
		_, res.Git = State(ctx, dir)
		res.Outcome, res.Message = "fast_forwarded", fmt.Sprintf("已快进 %d 个提交，和 %s 一致", n, st.Upstream)
	}
	return res, nil
}

var unsafeRef = regexp.MustCompile(`[^a-z0-9-]+`)

// WIP saves everything uncommitted in the checkout (untracked files too,
// .gitignore respected) as one commit on top of HEAD, and pushes it to
// sous/wip/<machine>-<time> on origin. The checkout, its index and its
// branch are not touched. Unpushed commits go along, since the commit sits
// on top of them.
func WIP(ctx context.Context, dir, machine string) (proto.RepoResult, error) {
	branch, st := State(ctx, dir)
	if st == nil || st.Head == "" {
		return proto.RepoResult{}, fmt.Errorf("%s 还没有提交，没法存成分支", dir)
	}
	if st.Changes+st.Untracked == 0 && st.Ahead == 0 && st.Upstream != "" {
		return proto.RepoResult{Outcome: "nothing", Message: "没有未提交或未推送的改动", Git: st}, nil
	}
	commit := st.Head
	if st.Changes+st.Untracked > 0 {
		tmp, err := os.MkdirTemp("", "sous-wip-")
		if err != nil {
			return proto.RepoResult{}, err
		}
		defer os.RemoveAll(tmp)
		env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
		if _, err := gitEnv(ctx, dir, env, "read-tree", "HEAD"); err != nil {
			return proto.RepoResult{}, err
		}
		if _, err := gitEnv(ctx, dir, env, "add", "-A"); err != nil {
			return proto.RepoResult{}, err
		}
		tree, err := gitEnv(ctx, dir, env, "write-tree")
		if err != nil {
			return proto.RepoResult{}, err
		}
		msg := fmt.Sprintf("WIP：%s 上 %s 分支未提交的改动（sous 存档）", machine, orHead(branch))
		if commit, err = gitEnv(ctx, dir, []string{"GIT_AUTHOR_NAME=sous", "GIT_AUTHOR_EMAIL=sous@localhost",
			"GIT_COMMITTER_NAME=sous", "GIT_COMMITTER_EMAIL=sous@localhost"}, "commit-tree", tree, "-p", st.Head, "-m", msg); err != nil {
			return proto.RepoResult{}, err
		}
	}
	name := strings.Trim(unsafeRef.ReplaceAllString(strings.ToLower(machine), "-"), "-")
	if name == "" {
		name = "machine"
	}
	ref := "sous/wip/" + name + "-" + time.Now().Format("20060102-150405")
	if out, err := push(ctx, dir, commit+":refs/heads/"+ref); err != nil {
		return proto.RepoResult{}, fmt.Errorf("推送失败：%s", firstLine(out, err))
	}
	return proto.RepoResult{Outcome: "pushed", Message: "已推送到 origin 的 " + ref + "（本地的文件和分支没有改动）",
		Branch: ref, Commit: commit, Git: st}, nil
}

func gitEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// push pushes a refspec to origin without asking for credentials.
func push(ctx context.Context, dir, refspec string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "push", "--quiet", "origin", refspec)
	cmd.Env = noPrompt()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// noPrompt disables terminal prompts and GCM interaction. It does not suppress
// OS dialogs from other helpers; background fetches must disable those helpers.
func noPrompt() []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

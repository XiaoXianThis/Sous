package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Git operations the supervisor asks for (decision 0022). Every branch and
// remote is named by the caller; nothing here picks one by itself. The
// commands are fixed. Each operation that moves a branch is written to a log
// (root/gitops.jsonl) so the last one can be undone while it is not pushed.

// ErrConflict means a merge would conflict; nothing was changed.
var ErrConflict = errors.New("合并有冲突")

// CommitTask commits what is left in a task's worktree. With squash, every
// commit since the task's base becomes one. It reports the branch's tip
// before and after.
func CommitTask(ctx context.Context, p Prepared, message string, squash bool) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitCommit, Branch: p.Branch}
	before, err := git(ctx, p.Dir, "rev-parse", "HEAD")
	if err != nil {
		return res, err
	}
	res.Before = before
	if message == "" {
		message = "sous: " + p.Branch
	}
	if _, err := Commit(ctx, p, message); err != nil {
		return res, fmt.Errorf("提交失败：%w", err)
	}
	if squash && p.Base != "" {
		if n, _ := git(ctx, p.Dir, "rev-list", "--count", p.Base+"..HEAD"); n != "" && n != "0" && n != "1" {
			if _, err := git(ctx, p.Dir, "reset", "--soft", p.Base); err != nil {
				return res, err
			}
			if _, err := Commit(ctx, p, message); err != nil {
				return res, fmt.Errorf("压成一个提交失败：%w", err)
			}
		}
	}
	res.After, _ = git(ctx, p.Dir, "rev-parse", "HEAD")
	if res.After == res.Before {
		res.Message = "没有新的改动要提交（" + p.Branch + "）"
		return res, nil
	}
	n, _ := git(ctx, p.Dir, "rev-list", "--count", p.Base+"..HEAD")
	res.Commits, _ = strconv.Atoi(n)
	res.Message = fmt.Sprintf("已在 %s 上提交（现在共 %d 个提交）", p.Branch, res.Commits)
	return res, nil
}

// MergeCheck says what a merge into the branch into would touch: the
// worktree where into is checked out ("" when nowhere), and an error when
// the merge cannot happen as it is.
func MergeCheck(ctx context.Context, p Prepared, into string) (checkout string, err error) {
	if into == "" {
		return "", errors.New("没有指定要合并到哪个分支")
	}
	if into == p.Branch {
		return "", errors.New("不能把任务分支合并到它自己")
	}
	if _, err := git(ctx, p.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+into); err != nil {
		return "", fmt.Errorf("这台机器上没有本地分支 %s", into)
	}
	checkout = worktreeOf(ctx, p.Repo, into)
	if checkout != "" {
		if out, _ := git(ctx, checkout, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(out) != "" {
			return checkout, fmt.Errorf("%s 在 %s 检出着，而且有未提交的改动；先提交或收起这些改动再合并", into, checkout)
		}
	}
	return checkout, nil
}

// MergeTask commits what is left in the task's worktree, optionally folds
// its commits into one, rebases the branch onto into and then moves into
// forward to it (fast-forward only). On a conflict it aborts the rebase,
// changes nothing and returns ErrConflict with the files in res.Conflicts.
// The task's worktree and branch are removed after the merge; the result
// keeps the branch's tip so an undo can bring it back.
func MergeTask(ctx context.Context, p Prepared, into, message string, squash bool) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitMerge, Branch: into, TaskBranch: p.Branch}
	checkout, err := MergeCheck(ctx, p, into)
	if err != nil {
		return res, err
	}
	if message == "" {
		message = "sous: " + p.Branch
	}
	if _, err := Commit(ctx, p, message); err != nil {
		return res, fmt.Errorf("提交剩下的改动失败：%w", err)
	}
	before, _ := git(ctx, p.Repo, "rev-parse", "refs/heads/"+into)
	res.Before = before
	if n, _ := git(ctx, p.Dir, "rev-list", "--count", into+"..HEAD"); n == "0" {
		return res, fmt.Errorf("%s 上没有 %s 之外的改动，没有可以合并的东西", p.Branch, into)
	}
	if squash && p.Base != "" {
		if n, _ := git(ctx, p.Dir, "rev-list", "--count", p.Base+"..HEAD"); n != "" && n != "0" && n != "1" {
			if _, err := git(ctx, p.Dir, "reset", "--soft", p.Base); err != nil {
				return res, err
			}
			if _, err := Commit(ctx, p, message); err != nil {
				return res, fmt.Errorf("压成一个提交失败：%w", err)
			}
		}
	}
	if _, err := git(ctx, p.Dir, "-c", "user.name="+authorName(ctx, p), "-c", "user.email="+authorEmail(ctx, p), "rebase", "-q", into); err != nil {
		out, _ := git(ctx, p.Dir, "diff", "--name-only", "--diff-filter=U")
		for _, f := range strings.Split(out, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				res.Conflicts = append(res.Conflicts, f)
			}
		}
		git(ctx, p.Dir, "rebase", "--abort")
		if len(res.Conflicts) == 0 {
			return res, fmt.Errorf("把 %s rebase 到 %s 上失败：%w", p.Branch, into, err)
		}
		res.Message = fmt.Sprintf("%s 和 %s 有冲突（%s），什么都没有改", p.Branch, into, strings.Join(res.Conflicts, "、"))
		return res, ErrConflict
	}
	tip, err := git(ctx, p.Dir, "rev-parse", "HEAD")
	if err != nil {
		return res, err
	}
	if checkout != "" {
		if _, err := git(ctx, checkout, "merge", "--ff-only", "-q", tip); err != nil {
			return res, fmt.Errorf("在 %s 里快进 %s 失败：%w", checkout, into, err)
		}
	} else if _, err := git(ctx, p.Repo, "update-ref", "refs/heads/"+into, tip, before); err != nil {
		return res, fmt.Errorf("快进 %s 失败：%w", into, err)
	}
	res.After = tip
	n, _ := git(ctx, p.Repo, "rev-list", "--count", before+".."+tip)
	res.Commits, _ = strconv.Atoi(n)
	git(ctx, p.Repo, "worktree", "remove", "--force", p.Dir)
	git(ctx, p.Repo, "branch", "-D", p.Branch)
	res.Message = fmt.Sprintf("已把 %s 合入 %s（快进，%d 个提交），没有推送", p.Branch, into, res.Commits)
	return res, nil
}

// worktreeOf is the directory where branch is checked out, or "".
func worktreeOf(ctx context.Context, top, branch string) string {
	out, err := git(ctx, top, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	dir := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			dir = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			return dir
		}
	}
	return ""
}

// PushRemote resolves the remote to push branch to: the given one, else
// the branch's upstream remote, else the only remote. With several remotes
// and no upstream it asks instead of guessing.
func PushRemote(ctx context.Context, top, branch, remote string) (string, error) {
	out, _ := git(ctx, top, "remote")
	remotes := strings.Fields(out)
	if remote != "" {
		for _, r := range remotes {
			if r == remote {
				return remote, nil
			}
		}
		return "", fmt.Errorf("没有叫 %s 的远端（有：%s）", remote, strings.Join(remotes, "、"))
	}
	if up, err := git(ctx, top, "config", "--get", "branch."+branch+".remote"); err == nil && up != "" && up != "." {
		return up, nil
	}
	switch len(remotes) {
	case 0:
		return "", errors.New("仓库没有任何远端，无法推送")
	case 1:
		return remotes[0], nil
	}
	return "", fmt.Errorf("%s 没有上游分支，仓库有多个远端（%s）：要说明推到哪一个", branch, strings.Join(remotes, "、"))
}

// PushBranch pushes a local branch to remote under the same name, never
// with force. A branch without an upstream gets this one.
func PushBranch(ctx context.Context, top, branch, remote string) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitPush, Branch: branch, Remote: remote}
	tip, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil || tip == "" {
		return res, fmt.Errorf("没有本地分支 %s", branch)
	}
	res.Before, _ = git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+branch)
	res.After = tip
	if res.Before == tip {
		res.Message = fmt.Sprintf("%s 已经和 %s/%s 一样，不用推送", branch, remote, branch)
		return res, nil
	}
	args := []string{"push", "--quiet"}
	if up, _ := git(ctx, top, "config", "--get", "branch."+branch+".remote"); up == "" {
		args = append(args, "-u")
	}
	args = append(args, remote, "refs/heads/"+branch+":refs/heads/"+branch)
	if out, err := gitNoPrompt(ctx, top, args...); err != nil {
		return res, fmt.Errorf("推送失败：%s", firstLine(out, err))
	}
	if res.Before != "" {
		n, _ := git(ctx, top, "rev-list", "--count", res.Before+".."+tip)
		res.Commits, _ = strconv.Atoi(n)
		res.Message = fmt.Sprintf("已把 %s 推送到 %s（%d 个新提交）", branch, remote, res.Commits)
	} else {
		res.Message = fmt.Sprintf("已把 %s 推送到 %s（新分支）", branch, remote)
	}
	return res, nil
}

func gitNoPrompt(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := execGit(ctx, dir, args...)
	cmd.Env = noPrompt()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// DiscardTask deletes a task's worktree and its branch.
func DiscardTask(ctx context.Context, p Prepared) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitDiscard, Branch: p.Branch}
	res.Before, _ = git(ctx, p.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+p.Branch)
	if _, err := os.Stat(p.Dir); err == nil {
		if _, err := git(ctx, p.Repo, "worktree", "remove", "--force", p.Dir); err != nil {
			return res, err
		}
	}
	if res.Before != "" {
		if _, err := git(ctx, p.Repo, "branch", "-D", p.Branch); err != nil {
			return res, err
		}
	}
	res.Message = "已删除任务分支 " + p.Branch + " 和它的 worktree"
	return res, nil
}

// Status reads where a checkout stands, for the supervisor to judge which
// branch the user means. No network access.
func Status(ctx context.Context, dir string) (proto.GitStatus, error) {
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return proto.GitStatus{}, fmt.Errorf("%s 不是 git 仓库", dir)
	}
	st := proto.GitStatus{Root: top}
	branch, gs := State(ctx, top)
	if gs != nil {
		st.Head, st.Upstream, st.Ahead, st.Behind = gs.Head, gs.Upstream, gs.Ahead, gs.Behind
		st.Changes, st.Untracked, st.FetchedAt = gs.Changes, gs.Untracked, gs.FetchedAt
	}
	if branch == "HEAD" || branch == "" {
		st.Detached = true
	} else {
		st.Branch = branch
	}
	st.RemoteDefault = remoteDefault(ctx, top)
	if out, err := git(ctx, top, "remote"); err == nil {
		st.Remotes = strings.Fields(out)
	}
	out, err := git(ctx, top, "for-each-ref", "--sort=-committerdate", "--count=12",
		"--format=%(refname:short)%09%(upstream:short)%09%(upstream:track,nobracket)%09%(committerdate:unix)%09%(contents:subject)", "refs/heads")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			f := strings.SplitN(line, "\t", 5)
			if len(f) < 5 || f[0] == "" {
				continue
			}
			b := proto.GitBranch{Name: f[0], Upstream: f[1], Subject: f[4]}
			b.When, _ = strconv.ParseInt(f[3], 10, 64)
			for _, part := range strings.Split(f[2], ",") {
				part = strings.TrimSpace(part)
				if v, ok := strings.CutPrefix(part, "ahead "); ok {
					b.Ahead, _ = strconv.Atoi(v)
				} else if v, ok := strings.CutPrefix(part, "behind "); ok {
					b.Behind, _ = strconv.Atoi(v)
				}
			}
			b.Worktree = worktreeOf(ctx, top, b.Name)
			st.Branches = append(st.Branches, b)
		}
	}
	return st, nil
}

// Heads lists origin's remote-tracking branches and their commits, at most
// 50, newest first.
func Heads(ctx context.Context, dir string) map[string]string {
	out, err := git(ctx, dir, "for-each-ref", "--sort=-committerdate", "--count=50", "--format=%(refname:strip=3) %(objectname)", "refs/remotes/origin")
	if err != nil || out == "" {
		return nil
	}
	heads := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, sha, ok := strings.Cut(line, " ")
		if ok && name != "HEAD" {
			heads[name] = sha
		}
	}
	return heads
}

// GitLog is the record of git operations sous did on one machine, kept to
// undo the last one.
type GitLog struct {
	mu   sync.Mutex
	path string
}

// GitLogEntry is one operation that moved a branch.
type GitLogEntry struct {
	At         int64  `json:"at"`
	Repo       string `json:"repo"`
	Op         string `json:"op"`
	Branch     string `json:"branch"`
	Before     string `json:"before"`
	After      string `json:"after"`
	TaskBranch string `json:"task_branch,omitempty"` // merge: the task branch it deleted
	Dir        string `json:"dir,omitempty"`         // commit: the worktree where the branch is
	Undone     bool   `json:"undone,omitempty"`
}

func OpenGitLog(root string) *GitLog { return &GitLog{path: filepath.Join(root, "gitops.jsonl")} }

// Add records an operation.
func (l *GitLog) Add(e GitLogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.At == 0 {
		e.At = time.Now().UnixMilli()
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, _ := json.Marshal(e)
	_, err = f.Write(append(raw, '\n'))
	return err
}

func (l *GitLog) entries() []GitLogEntry {
	f, err := os.Open(l.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []GitLogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e GitLogEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func (l *GitLog) rewrite(es []GitLogEntry) error {
	var b strings.Builder
	for _, e := range es {
		raw, _ := json.Marshal(e)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return os.WriteFile(l.path, []byte(b.String()), 0o600)
}

// Undo reverts the newest operation sous did in the repository top (on
// branch, when given) that is not undone yet, if its result was never
// pushed: the branch goes back to where it was, and a task branch that a
// merge deleted comes back.
func (l *GitLog) Undo(ctx context.Context, top, branch string) (proto.GitResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	res := proto.GitResult{Op: proto.GitUndo, Branch: branch}
	es := l.entries()
	idx := -1
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		if e.Undone || filepath.Clean(e.Repo) != filepath.Clean(top) || (branch != "" && e.Branch != branch) {
			continue
		}
		if e.Op == proto.GitPush {
			return res, fmt.Errorf("最近一次操作是推送 %s：已经推送的提交不能撤回", e.Branch)
		}
		idx = i
		break
	}
	if idx < 0 {
		return res, errors.New("没有 sous 做过、还能撤回的 git 操作")
	}
	e := es[idx]
	res.Branch = e.Branch
	if out, _ := git(ctx, top, "branch", "-r", "--contains", e.After); strings.TrimSpace(out) != "" {
		return res, fmt.Errorf("%s 已经推送到远端（%s），不能撤回", short(e.After), strings.Fields(out)[0])
	}
	cur, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+e.Branch)
	if err != nil {
		return res, fmt.Errorf("分支 %s 已经不在了", e.Branch)
	}
	if cur != e.After {
		return res, fmt.Errorf("%s 在那次操作之后又变过（现在是 %s），不能自动撤回", e.Branch, short(cur))
	}
	if wt := worktreeOf(ctx, top, e.Branch); wt != "" {
		if out, _ := git(ctx, wt, "status", "--porcelain", "--untracked-files=no"); strings.TrimSpace(out) != "" {
			return res, fmt.Errorf("%s 在 %s 检出着，而且有未提交的改动；先处理这些改动", e.Branch, wt)
		}
		mode := "--keep"
		if e.Op == proto.GitCommit {
			mode = "--soft" // keep the committed changes in the worktree
		}
		if _, err := git(ctx, wt, "reset", "-q", mode, e.Before); err != nil {
			return res, err
		}
	} else if _, err := git(ctx, top, "update-ref", "refs/heads/"+e.Branch, e.Before, e.After); err != nil {
		return res, err
	}
	if e.TaskBranch != "" {
		if _, err := git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+e.TaskBranch); err != nil {
			git(ctx, top, "branch", e.TaskBranch, e.After)
		}
	}
	es[idx].Undone = true
	if err := l.rewrite(es); err != nil {
		return res, err
	}
	res.Before, res.After, res.TaskBranch = e.After, e.Before, e.TaskBranch
	res.Message = fmt.Sprintf("已撤回对 %s 的%s：%s 回到了 %s", e.Branch, opName[e.Op], e.Branch, short(e.Before))
	if e.TaskBranch != "" {
		res.Message += "，任务分支 " + e.TaskBranch + " 恢复了"
	}
	return res, nil
}

var opName = map[string]string{proto.GitCommit: "提交", proto.GitMerge: "合并", proto.GitPush: "推送"}

// Branches lists local branch names, sorted.
func Branches(ctx context.Context, top string) []string {
	out, _ := git(ctx, top, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	bs := strings.Fields(out)
	sort.Strings(bs)
	return bs
}

func execGit(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
}

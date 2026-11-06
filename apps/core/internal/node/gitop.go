package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

// gitOp runs one git operation the supervisor asked for (decision 0022).
// The commands are fixed here; the Hub only names the branches. Every step
// that changes something goes through the policy like an agent's command,
// so the permission mode decides who approves it.
func (r *run) gitOp() {
	op := r.spec.Git
	if op == nil || r.spec.RepoPath == "" {
		r.end("failed", "", "", "", errors.New("git 操作缺少参数"))
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Minute)
	defer cancel()
	var res proto.GitResult
	var err error
	switch {
	case op.Op == proto.GitCommit && op.InPlace:
		res, err = r.commitInPlace(ctx, op)
	case op.Op == proto.GitCommit, op.Op == proto.GitMerge, op.Op == proto.GitPR, op.Op == proto.GitDiscard:
		res, err = r.taskGitOp(ctx, op)
	case op.Op == proto.GitPush:
		res, err = r.pushOp(ctx, op)
	case op.Op == proto.GitUndo:
		res, err = r.undoOp(ctx, op)
	case op.Op == proto.GitRestore:
		res, err = r.restoreOp(ctx, op)
	case op.Op == proto.GitHealth:
		res, err = r.healthOp(ctx, op)
	default:
		err = fmt.Errorf("不认识的 git 操作 %q", op.Op)
	}
	if res.Op == "" {
		res.Op = op.Op
	}
	r.d.rescan() // the Hub learns the new state of the checkout (and deploy branches) soon
	if err != nil {
		status := r.failureStatus()
		if errors.Is(err, errRefused) {
			status = "cancelled"
		}
		if res.Message == "" {
			res.Message = err.Error()
		}
		r.endGit(status, res, err)
		return
	}
	r.endGit("succeeded", res, nil)
}

var errRefused = errors.New("没有获得许可")

func (r *run) endGit(status string, res proto.GitResult, err error) {
	if r.isShutdown() {
		return
	}
	e := proto.RunEnded{RunID: r.spec.RunID, Status: status, Summary: res.Message, Git: &res, PRURL: res.PRURL}
	if err != nil {
		e.Error = err.Error()
		if r.ctx.Err() != nil {
			e.Error = r.stopReasonText()
		}
	}
	r.d.ob.reliable(proto.New(proto.TRunEnded, e))
	r.d.forget(r.spec.RunID)
	slog.Info("daemon: git op ended", "run", r.spec.RunID, "op", res.Op, "status", status, "err", err)
}

// allow asks the policy (and maybe the user) for one command.
func (r *run) allow(ask, command, dir, detail string) error {
	ok, err := r.permit(ask, command, dir, detail)
	if err != nil {
		return err
	}
	if !ok {
		if r.ctx.Err() != nil {
			return errors.New(r.stopReasonText())
		}
		return fmt.Errorf("%w：%s", errRefused, ask)
	}
	return nil
}

// taskWorktree finds the task's worktree (recreating it from the branch
// when its directory is gone). It never creates a new branch.
func (r *run) taskWorktree(ctx context.Context) (workspace.Prepared, error) {
	if r.spec.TaskID == "" || r.spec.Branch == "" {
		return workspace.Prepared{}, errNoTask
	}
	ws, err := workspace.PrepareTaskOn(ctx, r.d.root, r.spec.RepoPath, r.spec.TaskID, r.spec.Branch, "", workspace.Start{})
	if errors.Is(err, workspace.ErrNoStart) {
		return ws, fmt.Errorf("任务分支 %s 已经不在了", r.spec.Branch)
	}
	return ws, err
}

func (r *run) taskGitOp(ctx context.Context, op *proto.GitOp) (proto.GitResult, error) {
	res := proto.GitResult{Op: op.Op}
	ws, err := r.taskWorktree(ctx)
	if err != nil {
		return res, err
	}
	r.markStarted(ws)
	log := workspace.OpenGitLog(r.d.root)
	switch op.Op {
	case proto.GitCommit:
		if err := r.allow("提交任务分支 "+ws.Branch, "git commit", ws.Dir, "将在 "+ws.Dir+" 执行 git add -A 和 git commit"); err != nil {
			return res, err
		}
		r.status("正在提交")
		res, err = workspace.CommitTask(ctx, ws, firstNonEmpty(op.Message, r.spec.Title), op.Squash)
		if err == nil && res.Before != res.After {
			log.Add(workspace.GitLogEntry{Repo: ws.Repo, Op: proto.GitCommit, Branch: ws.Branch, Before: res.Before, After: res.After, Dir: ws.Dir})
		}
		return res, err

	case proto.GitMerge:
		checkout, err := workspace.MergeCheck(ctx, ws, op.Into)
		if err != nil {
			return res, err
		}
		cmd := fmt.Sprintf("git update-ref refs/heads/%s %s", op.Into, ws.Branch)
		where := op.Into + " 没有在任何地方检出，只移动分支"
		if checkout != "" {
			cmd = fmt.Sprintf("git -C %s merge --ff-only %s", checkout, ws.Branch)
			where = op.Into + " 在 " + checkout + " 检出着，那里的文件会更新"
		}
		detail := fmt.Sprintf("把任务分支 %s rebase 到 %s 上，然后快进 %s（%s）。\n将执行：\n  git rebase %s\n  %s\n完成后删除任务的 worktree 和分支；没推送之前可以撤回。",
			ws.Branch, op.Into, op.Into, where, op.Into, cmd)
		if op.Squash {
			detail = "先把任务的提交压成一个。\n" + detail
		}
		if err := r.allow("把 "+ws.Branch+" 合并到 "+op.Into, cmd, ws.Dir, detail); err != nil {
			return res, err
		}
		r.status("正在合并到 " + op.Into)
		res, err = workspace.MergeTask(ctx, ws, op.Into, firstNonEmpty(op.Message, r.spec.Title), op.Squash)
		if err == nil {
			log.Add(workspace.GitLogEntry{Repo: ws.Repo, Op: proto.GitMerge, Branch: op.Into, Before: res.Before, After: res.After, TaskBranch: res.TaskBranch, TaskTip: res.TaskTip})
		}
		return res, err

	case proto.GitPR:
		if op.Base == "" {
			return res, errors.New("没有指定 PR 的目标分支")
		}
		push := "git push -u origin " + ws.Branch
		detail := fmt.Sprintf("将执行：\n  %s\n  gh pr create --head %s --base %s", push, ws.Branch, op.Base)
		if commits := workspace.Commits(ctx, ws); commits != "" {
			detail += "\n\n提交：\n" + commits
		}
		if err := r.allow("push "+ws.Branch+" 并创建 PR", push, ws.Dir, detail); err != nil {
			return res, err
		}
		r.status("正在提交剩下的改动")
		if _, err := workspace.Commit(ctx, ws, firstNonEmpty(op.Title, r.spec.Title)); err != nil {
			return res, fmt.Errorf("提交失败：%w", err)
		}
		if workspace.Commits(ctx, ws) == "" {
			return res, errors.New("分支上没有任何改动，没有可以提 PR 的东西")
		}
		r.status("正在 push " + ws.Branch)
		if err := workspace.Push(ctx, ws, false); err != nil {
			return res, fmt.Errorf("push 失败：%w", err)
		}
		ws.BaseBranch = op.Base
		r.status("正在创建 PR")
		url, conflict, err := workspace.PullRequest(ctx, ws, firstNonEmpty(op.Title, r.spec.Title), firstNonEmpty(op.Body, r.prBody(workspace.Commits(ctx, ws))))
		if err != nil {
			return res, err
		}
		res = proto.GitResult{Op: proto.GitPR, Branch: ws.Branch, PRURL: url, Remote: "origin",
			Message: fmt.Sprintf("已 push %s 并创建 PR：%s", ws.Branch, url)}
		if conflict {
			res.Message += "（和 " + op.Base + " 有冲突）"
		}
		return res, nil

	case proto.GitDiscard:
		cmd := "git branch -D " + ws.Branch
		if err := r.allow("删除任务分支 "+ws.Branch, cmd, ws.Dir, "将删除任务的 worktree（"+ws.Dir+"）和分支 "+ws.Branch+"，上面没合并的改动会丢失。"); err != nil {
			return res, err
		}
		return workspace.DiscardTask(ctx, ws)
	}
	return res, fmt.Errorf("不认识的 git 操作 %q", op.Op)
}

func (r *run) pushOp(ctx context.Context, op *proto.GitOp) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitPush, Branch: op.Branch}
	top := r.spec.RepoPath
	r.markStarted(workspace.Prepared{Dir: top})
	if op.Branch == "" {
		return res, errors.New("没有指定要推送的分支")
	}
	remote, err := workspace.PushRemote(ctx, top, op.Branch, op.Remote)
	if err != nil {
		return res, err
	}
	cmd := "git push " + remote + " " + op.Branch
	if err := r.allow("推送 "+op.Branch+" 到 "+remote, cmd, top, "将在 "+top+" 执行：\n  "+cmd); err != nil {
		return res, err
	}
	r.status("正在推送 " + op.Branch)
	res, err = workspace.PushBranch(ctx, top, op.Branch, remote)
	if err == nil && res.Before != res.After {
		workspace.OpenGitLog(r.d.root).Add(workspace.GitLogEntry{Repo: top, Op: proto.GitPush, Branch: op.Branch, Before: res.Before, After: res.After})
	}
	return res, err
}

func (r *run) undoOp(ctx context.Context, op *proto.GitOp) (proto.GitResult, error) {
	top := r.spec.RepoPath
	r.markStarted(workspace.Prepared{Dir: top})
	what := "最近一次 git 操作"
	if op.Branch != "" {
		what = op.Branch + " 上" + what
	}
	cmd := "git update-ref"
	if op.Branch != "" {
		cmd = "git reset --keep (" + op.Branch + ")"
	}
	if err := r.allow("撤回 sous 在 "+what, cmd, top, "把分支放回 sous 那次操作之前的位置；已经推送的不能撤回。"); err != nil {
		return proto.GitResult{Op: proto.GitUndo}, err
	}
	return workspace.OpenGitLog(r.d.root).Undo(ctx, top, op.Branch)
}

// commitInPlace commits an in-place task's changes in the project checkout,
// on its current branch.
func (r *run) commitInPlace(ctx context.Context, op *proto.GitOp) (proto.GitResult, error) {
	dir := r.spec.RepoPath
	r.markStarted(workspace.Prepared{Dir: dir})
	if err := r.allow("在 "+dir+" 提交任务的改动", "git commit", dir,
		"只提交这个任务改过的文件、按任务结束时的内容提交，提交到这个目录当前检出的分支；你自己的改动（包括任务结束后对同一文件的改动）留在工作区，不会被提交。"); err != nil {
		return proto.GitResult{Op: proto.GitCommit}, err
	}
	res, err := workspace.CommitInPlace(ctx, r.d.root, dir, op.BaseCommit, r.spec.TaskID, firstNonEmpty(op.Message, r.spec.Title))
	if err == nil && res.Before != res.After && res.After != "" {
		top := dir
		if t, e := workspace.Run(ctx, dir, "git", "rev-parse", "--show-toplevel"); e == nil {
			top = strings.TrimSpace(t)
		}
		workspace.OpenGitLog(r.d.root).Add(workspace.GitLogEntry{Repo: top, Op: proto.GitCommit, Branch: res.Branch, Before: res.Before, After: res.After})
	}
	return res, err
}

func (r *run) restoreOp(ctx context.Context, op *proto.GitOp) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitRestore}
	dir := r.spec.RepoPath
	r.markStarted(workspace.Prepared{Dir: dir})
	if op.BaseCommit == "" {
		return res, errors.New("这个任务没有快照，无法撤销")
	}
	cmd := "git checkout " + op.BaseCommit + " -- <任务改过的文件>; rm <任务新增的文件>"
	if err := r.allow("把 "+dir+" 里这个任务的改动撤销", cmd, dir,
		"只撤销这个任务改过的文件：改过的恢复成任务开始前的样子，任务新建的删除。你自己新建或改过的文件不动；任务结束后又被改过的文件也不动，会列出来。"); err != nil {
		return res, err
	}
	stat, err := workspace.Restore(ctx, r.d.root, dir, op.BaseCommit, r.spec.TaskID)
	if err != nil {
		return res, err
	}
	res.Message = "已把 " + dir + " 恢复到任务开始前"
	if stat = strings.TrimSpace(stat); stat != "" {
		res.Message += "（" + stat + "）"
	}
	return res, nil
}

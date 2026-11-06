package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Git operations (decision 0022). A task ends with its changes on its
// branch or in place; committing, merging, pushing, pull requests, undo and
// discarding are separate operations the supervisor runs when the user asks.
// Each one is a git run: the node executes fixed commands, and every step
// that changes something goes through the node's policy, so the permission
// mode decides who approves it. Nothing picks a branch or a remote by
// itself: the caller names them.

// GitWait is how long a tool call waits for a git operation (including an
// approval) before it lets the result come as a report instead.
var GitWait = 5 * time.Minute

// GitInput asks for one git operation.
type GitInput struct {
	Op        string // commit / merge / push / pr / undo / discard / restore
	TaskID    string // commit, merge, pr, discard, restore; push and undo may name it for its project
	ProjectID string // push, undo
	NodeID    string // push, undo: the machine; needed when the project is on several
	Into      string // merge
	Base      string // pr
	Branch    string // push, undo
	Remote    string // push
	Squash    bool
	Message   string
	Title     string
	Body      string
	ThreadID  string
	Actor     string
}

var taskOps = map[string]bool{proto.GitCommit: true, proto.GitMerge: true, proto.GitPR: true, proto.GitDiscard: true, proto.GitRestore: true}

// StartGit checks a git operation and starts its run.
func (e *Engine) StartGit(ctx context.Context, in GitInput) (store.Run, error) {
	cr := nodehub.CreateRun{ThreadID: in.ThreadID, Purpose: event.PurposeGit}
	op := &proto.GitOp{Op: in.Op, Into: strings.TrimSpace(in.Into), Base: strings.TrimSpace(in.Base), Branch: strings.TrimSpace(in.Branch),
		Remote: strings.TrimSpace(in.Remote), Squash: in.Squash, Message: strings.TrimSpace(in.Message), Title: strings.TrimSpace(in.Title), Body: in.Body}
	var proj store.Project
	if taskOps[in.Op] {
		if in.TaskID == "" {
			return store.Run{}, fmt.Errorf("%w: 这个 git 操作要指定任务", ErrInvalid)
		}
		t, err := e.store().Task(ctx, in.TaskID)
		if err != nil {
			return store.Run{}, fmt.Errorf("%w: 没有任务 %s", ErrInvalid, in.TaskID)
		}
		if active(t.Status) {
			return store.Run{}, fmt.Errorf("%w: 任务「%s」还在执行，等它结束再做 git 操作", ErrConflict, t.Title)
		}
		work := e.lastRun(ctx, t.ID, event.PurposeWork, event.PurposeRebase)
		if work.NodeID == "" || work.RepoPath == "" {
			return store.Run{}, fmt.Errorf("%w: 任务「%s」还没有在哪台机器上做过", ErrConflict, t.Title)
		}
		if in.Op == proto.GitCommit && t.Place == event.PlaceInPlace {
			// An in-place task's changes are committed in the checkout itself,
			// on whatever branch it has checked out.
			if t.BaseCommit == "" {
				return store.Run{}, fmt.Errorf("%w: 这个任务没有快照，分不清哪些是它的改动", ErrConflict)
			}
			if t.GitState == event.GitRestored {
				return store.Run{}, fmt.Errorf("%w: 这个任务的改动已经撤销了", ErrConflict)
			}
			op.BaseCommit, op.InPlace = t.BaseCommit, true
		} else if in.Op == proto.GitRestore {
			if t.Place != event.PlaceInPlace || t.BaseCommit == "" {
				return store.Run{}, fmt.Errorf("%w: 只有就地执行、拍过快照的任务能恢复", ErrInvalid)
			}
			if t.GitState == event.GitRestored {
				return store.Run{}, fmt.Errorf("%w: 这个任务的改动已经撤销过了", ErrConflict)
			}
			op.BaseCommit, op.InPlace = t.BaseCommit, true
		} else {
			if t.Place != event.PlaceWorktree || t.Branch == "" {
				return store.Run{}, fmt.Errorf("%w: 任务「%s」没有自己的分支（它%s）", ErrInvalid, t.Title, placeDoing[t.Place])
			}
			switch t.GitState {
			case event.GitMerged, event.GitDiscarded:
				return store.Run{}, fmt.Errorf("%w: 任务的分支已经%s", ErrConflict, gitStateName[t.GitState])
			}
			op.BaseCommit = t.BaseCommit
		}
		switch {
		case in.Op == proto.GitMerge && op.Into == "":
			return store.Run{}, fmt.Errorf("%w: 要说明合并到哪个分支（into）", ErrInvalid)
		case in.Op == proto.GitPR && op.Base == "":
			return store.Run{}, fmt.Errorf("%w: 要说明 PR 的目标分支（base）", ErrInvalid)
		}
		proj, _ = e.store().Project(ctx, t.ProjectID)
		cr.TaskID, cr.NodeID, cr.RepoPath, cr.Branch, cr.Title = t.ID, work.NodeID, work.RepoPath, t.Branch, t.Title
		cr.Prompt = t.Detail
		cr.PermissionMode = firstNonEmpty(t.PermissionMode, proj.DefaultMode, e.setting(ctx, SettingDefaultMode), proto.ModeAuto)
		if in.ThreadID == "" {
			cr.ThreadID = t.OriginThreadID
		}
	} else {
		switch in.Op {
		case proto.GitPush:
			if op.Branch == "" {
				return store.Run{}, fmt.Errorf("%w: 要说明推送哪个分支（branch）", ErrInvalid)
			}
		case proto.GitUndo:
		default:
			return store.Run{}, fmt.Errorf("%w: 不认识的 git 操作 %q", ErrInvalid, in.Op)
		}
		pid := in.ProjectID
		if pid == "" && in.TaskID != "" {
			if t, err := e.store().Task(ctx, in.TaskID); err == nil {
				pid = t.ProjectID
			}
		}
		p, err := e.store().Project(ctx, pid)
		if err != nil || pid == "" {
			return store.Run{}, fmt.Errorf("%w: 要指定项目", ErrInvalid)
		}
		proj = p
		place, err := e.placeOn(ctx, p, in.NodeID)
		if err != nil {
			return store.Run{}, err
		}
		cr.NodeID, cr.RepoPath = place.NodeID, place.Path
		cr.PermissionMode = firstNonEmpty(p.DefaultMode, e.setting(ctx, SettingDefaultMode), proto.ModeAuto)
	}
	if cr.ThreadID == "" {
		cr.ThreadID, _ = e.store().MainThreadID(ctx)
	}
	if !e.Nodes.Online(cr.NodeID) {
		return store.Run{}, fmt.Errorf("%w: %s 现在不在线，git 操作要在那台机器上做", ErrConflict, e.nodeName(ctx, cr.NodeID))
	}
	cr.Git = op
	// One git operation at a time per task and per checkout: two at once
	// fight over the index lock, and the undo log would not match what
	// happened (review M4). The check and the creation go together.
	gitStartMu.Lock()
	defer gitStartMu.Unlock()
	if busy := e.gitBusy(ctx, cr); busy != "" {
		return store.Run{}, fmt.Errorf("%w: %s", ErrConflict, busy)
	}
	return e.Nodes.Create(ctx, cr)
}

var gitStartMu sync.Mutex

// gitBusy says which git operation of the same task or the same checkout is
// still under way, or "".
func (e *Engine) gitBusy(ctx context.Context, cr nodehub.CreateRun) string {
	runs, err := e.store().Runs(ctx, store.RunFilter{NodeID: cr.NodeID, Statuses: []string{event.RunStatusQueued, event.RunStatusRunning}})
	if err != nil {
		return ""
	}
	for _, r := range runs {
		if r.Purpose != event.PurposeGit {
			continue
		}
		if (cr.TaskID != "" && r.TaskID == cr.TaskID) || (r.RepoPath != "" && filepath.Clean(r.RepoPath) == filepath.Clean(cr.RepoPath)) {
			name := gitOpName[gitOpOf(r)]
			if name == "" {
				name = "git 操作"
			}
			return "上一个「" + name + "」还没结束，等它完成再做下一个"
		}
	}
	return ""
}

var placeDoing = map[string]string{event.PlaceInPlace: "是就地执行的", event.PlaceScratch: "在临时目录里做的", "": "还没定在哪儿做"}

// placeOn is the project's directory on the named machine, or on its only
// machine. With several machines and none named it asks instead of picking.
func (e *Engine) placeOn(ctx context.Context, p store.Project, nodeID string) (event.ProjectNode, error) {
	if nodeID != "" {
		for _, n := range p.Nodes {
			if n.NodeID == nodeID {
				return n, nil
			}
		}
		return event.ProjectNode{}, fmt.Errorf("%w: 项目 %s 不在 %s 上", ErrInvalid, p.Name, e.nodeName(ctx, nodeID))
	}
	switch len(p.Nodes) {
	case 0:
		return event.ProjectNode{}, fmt.Errorf("%w: 项目 %s 还不在任何机器上", ErrConflict, p.Name)
	case 1:
		return p.Nodes[0], nil
	}
	var names []string
	for _, n := range p.Nodes {
		names = append(names, e.nodeName(ctx, n.NodeID))
	}
	return event.ProjectNode{}, fmt.Errorf("%w: 项目 %s 在几台机器上（%s），要说明是哪一台", ErrInvalid, p.Name, strings.Join(names, "、"))
}

func (e *Engine) nodeName(ctx context.Context, id string) string {
	if n, err := e.store().Node(ctx, id); err == nil && n.Name != "" {
		return n.Name
	}
	return id
}

// Git starts a git operation and waits for it (and any approval it needs)
// up to wait. done is false when it is still going; its result then comes
// as a report in the thread.
func (e *Engine) Git(ctx context.Context, in GitInput, wait time.Duration) (r store.Run, done bool, err error) {
	ch := make(chan event.Event, 4)
	r, err = e.StartGit(ctx, in)
	if err != nil {
		return r, false, err
	}
	e.wait(r.ID, ch)
	defer e.unwait(r.ID)
	if cur, err := e.store().Run(ctx, r.ID); err == nil && isFinalRun(cur.Status) {
		return cur, true, nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return r, false, ctx.Err()
		case <-t.C:
			return r, false, nil
		case ev := <-ch:
			if ev.Type == event.RunEnded {
				cur, err := e.store().Run(ctx, r.ID)
				return cur, err == nil, err
			}
		}
	}
}

func isFinalRun(status string) bool {
	switch status {
	case event.RunStatusSucceeded, event.RunStatusFailed, event.RunStatusCancelled, event.RunStatusInterrupted:
		return true
	}
	return false
}

// wait and unwait register a channel for a run's run.turn and run.ended.
func (e *Engine) wait(runID string, ch chan event.Event) {
	e.wmu.Lock()
	e.waiters[runID] = ch
	e.wmu.Unlock()
}

func (e *Engine) unwait(runID string) {
	e.wmu.Lock()
	delete(e.waiters, runID)
	e.wmu.Unlock()
}

func (e *Engine) waiting(runID string) bool {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	_, ok := e.waiters[runID]
	return ok
}

// onRunEvent hands run.turn and run.ended to whoever waits for the run, and
// takes care of a consultant's answers that nobody waits for any more.
func (e *Engine) onRunEvent(ev event.Event) {
	if ev.Type != event.RunEnded && ev.Type != event.RunTurn {
		return
	}
	e.wmu.Lock()
	ch, ok := e.waiters[ev.RunID]
	e.wmu.Unlock()
	if ok {
		select {
		case ch <- ev:
		default:
		}
		return
	}
	if ev.Type == event.RunTurn {
		go e.lateAnswer(ev)
	}
}

// afterGit records what a git operation did to its task and, when no tool
// call waits for it any more, reports it in the thread.
func (e *Engine) afterGit(ctx context.Context, r store.Run) error {
	if gitOpOf(r) == proto.GitHealth && r.TaskID != "" {
		return e.afterHealth(ctx, r)
	}
	res := r.GitResult
	if res == nil {
		res = &proto.GitResult{Op: firstNonEmpty(gitOpOf(r), "git")}
	}
	if r.Status == event.RunStatusSucceeded && r.TaskID != "" {
		if t, err := e.store().Task(ctx, r.TaskID); err == nil {
			switch res.Op {
			case proto.GitMerge:
				e.gitEvent(ctx, t, event.GitMerged, "", r.ID)
			case proto.GitPR:
				e.gitEvent(ctx, t, event.GitPR, res.PRURL, r.ID)
			case proto.GitDiscard:
				e.gitEvent(ctx, t, event.GitDiscarded, "", r.ID)
			case proto.GitRestore:
				e.gitEvent(ctx, t, event.GitRestored, "", r.ID)
			}
		}
	}
	if r.Status == event.RunStatusSucceeded && res.Op == proto.GitUndo && res.TaskBranch != "" {
		// An undone merge brings the task branch back.
		if tasks, err := e.store().Tasks(ctx, store.TaskFilter{Limit: 5000}); err == nil {
			for _, t := range tasks {
				if t.Branch == res.TaskBranch && t.GitState == event.GitMerged {
					e.gitEvent(ctx, t, event.GitUnmerged, "", r.ID)
				}
			}
		}
	}
	if r.Status == event.RunStatusSucceeded && res.Op == proto.GitPush {
		go e.CheckDeploys(context.WithoutCancel(ctx))
	}
	if !e.waiting(r.ID) {
		e.postThread(ctx, r.ThreadID, r.TaskID, GitReport(r))
	}
	return nil
}

func gitOpOf(r store.Run) string {
	if r.Git != nil {
		return r.Git.Op
	}
	return ""
}

// GitReport is the one sentence about a finished git operation.
func GitReport(r store.Run) string {
	res := r.GitResult
	switch {
	case r.Status == event.RunStatusSucceeded && res != nil && res.Message != "":
		return res.Message
	case res != nil && len(res.Conflicts) > 0:
		return res.Message + "。可以让 agent 把任务分支 rebase 到目标分支上解决冲突（task_dispatch 时在说明里写清楚），之后再合并。"
	case r.Status == event.RunStatusCancelled:
		return "git 操作没有做：" + firstNonEmpty(r.Error, "被取消或没有获得许可")
	default:
		msg := "git 操作失败"
		if op := gitOpOf(r); op != "" {
			msg = gitOpName[op] + "失败"
		}
		return msg + "：" + firstNonEmpty(r.Error, r.Summary, "原因不明")
	}
}

var gitOpName = map[string]string{proto.GitCommit: "提交", proto.GitMerge: "合并", proto.GitPush: "推送", proto.GitPR: "开 PR",
	proto.GitUndo: "撤回", proto.GitDiscard: "删除分支", proto.GitRestore: "撤销改动", proto.GitHealth: "健康检查"}

// postThread posts a report in a thread (the main thread when empty).
func (e *Engine) postThread(ctx context.Context, thread, taskID, text string) {
	if thread == "" {
		thread, _ = e.store().MainThreadID(ctx)
	}
	t := store.Task{ID: taskID, OriginThreadID: thread}
	e.post(ctx, t, text, false)
}

// GitStatus reads where a project's checkout stands on a machine.
func (e *Engine) GitStatus(ctx context.Context, projectID, nodeID string) (proto.GitStatus, string, error) {
	p, err := e.store().Project(ctx, projectID)
	if err != nil {
		return proto.GitStatus{}, "", fmt.Errorf("%w: 没有项目 %s", ErrInvalid, projectID)
	}
	place, err := e.placeOn(ctx, p, nodeID)
	if err != nil {
		return proto.GitStatus{}, "", err
	}
	st, err := e.Nodes.GitStatus(ctx, place.NodeID, place.Path)
	if errors.Is(err, nodehub.ErrOffline) {
		return st, place.NodeID, fmt.Errorf("%w: %s 不在线", ErrConflict, e.nodeName(ctx, place.NodeID))
	}
	return st, place.NodeID, err
}

func logGit(r store.Run, err error) {
	if err != nil {
		slog.Warn("task: git operation", "run", r.ID, "err", err)
	}
}

// Package task owns tasks (docs/decisions/0011): their state machine and
// dependencies, dispatching them to machines through the router, and what
// happens when a run ends — review, rework, delivery as a pull request,
// rebase on conflict, and the report back to the thread the task came from.
//
// The engine reacts to stored events through the effects queue, so a step
// that was due when the Hub stopped runs after it restarts. Every step is
// idempotent: it re-reads the task and does nothing if it already moved on.
package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	projectkey "github.com/XiaoXianThis/Sous/apps/core/internal/project"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/router"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	// MaxWorkRuns bounds how often a task is sent back for rework.
	MaxWorkRuns = 3
	// MaxRebases bounds automatic rebases of a conflicting pull request.
	MaxRebases = 2
	// MaxRecoveries bounds automatic restarts of interrupted runs per dispatch.
	MaxRecoveries = 1
	// sweepEvery retries queued tasks even when no event wakes the engine.
	sweepEvery = time.Minute
)

// Settings keys the engine reads.
const (
	SettingDefaultMode  = "default_mode"
	SettingDefaultAgent = "default_agent"
)

var (
	ErrInvalid  = errors.New("invalid task")
	ErrConflict = errors.New("task cannot do that now")
)

// Model is a provider and model for one role.
type Model struct {
	Provider provider.Provider
	Name     string
}

type Engine struct {
	Log      *eventlog.Log
	Nodes    *nodehub.Hub
	Reviewer Model // nil provider: every run passes review
	Approver Model // nil provider: every AI approval goes to the user

	// Beat, when set, is called on every loop iteration (for the watchdog).
	Beat func()

	mu   sync.Mutex // serializes dispatching, so a run slot is never given twice
	kick chan struct{}
}

func New(log *eventlog.Log, nodes *nodehub.Hub) *Engine {
	return &Engine{Log: log, Nodes: nodes, kick: make(chan struct{}, 1)}
}

func (e *Engine) store() *store.Store { return e.Log.Store }

// Start listens for events that can unblock queued tasks or change a
// task's approval state, and sweeps queued tasks until ctx ends.
func (e *Engine) Start(ctx context.Context) {
	if err := e.Seed(ctx); err != nil {
		slog.Error("task: seed the misc project and built-in personas", "err", err)
	}
	go e.watchMerges(ctx)
	approvals := make(chan event.Event, 256)
	e.Log.Listen(func(ev event.Event) {
		switch ev.Type {
		case event.NodeOnline, event.NodeInventory, event.RunEnded, event.TaskCreated, event.ProjectPut:
			e.Sweep()
			if ev.Type == event.NodeOnline {
				var p event.NodePayload
				if ev.Decode(&p) == nil {
					go e.cleanupNode(ctx, p.NodeID)
				}
			}
		case event.TaskStatus:
			var p event.TaskStatusPayload
			if ev.Decode(&p) == nil && (p.To == event.TaskDone || p.To == event.TaskCancelled || p.To == event.TaskFailed) {
				e.Sweep() // dependents may be free now
			}
		case event.ApprovalRequested, event.ApprovalDecided, event.RunPaused, event.RunResumed:
			if ev.TaskID != "" {
				select {
				case approvals <- ev:
				default:
				}
			}
		}
	})
	go func() {
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		e.sweep(ctx)
		for {
			if e.Beat != nil {
				e.Beat()
			}
			select {
			case <-ctx.Done():
				return
			case ev := <-approvals:
				if ev.Type == event.RunPaused || ev.Type == event.RunResumed {
					e.pauseChanged(ctx, ev)
				} else {
					e.approvalChanged(ctx, ev.TaskID)
				}
			case <-e.kick:
				e.sweep(ctx)
			case <-t.C:
				e.sweep(ctx)
			}
		}
	}()
}

// Sweep asks the engine to retry queued tasks soon.
func (e *Engine) Sweep() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// sweep tries to start every queued task, by priority.
func (e *Engine) sweep(ctx context.Context) {
	queued, err := e.store().Tasks(ctx, store.TaskFilter{Statuses: []string{event.TaskQueued}})
	if err != nil {
		slog.Error("task: list queued", "err", err)
		return
	}
	for _, t := range queued {
		if ctx.Err() != nil {
			return
		}
		if _, err := e.tryStart(ctx, t.ID); err != nil {
			slog.Warn("task: dispatch failed", "task", t.ID, "err", err)
		}
	}
}

// approvalChanged moves a task between running and waiting_approval.
func (e *Engine) approvalChanged(ctx context.Context, taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store().Task(ctx, taskID)
	if err != nil {
		return
	}
	pending, _ := e.store().Approvals(ctx, "pending", "")
	waiting := false
	for _, a := range pending {
		waiting = waiting || a.TaskID == taskID
	}
	switch {
	case waiting && t.Status == event.TaskRunning:
		e.transition(ctx, t, event.TaskWaitingApproval, "等你审批", "", event.ActorSystem, keepNext(t))
	case !waiting && t.Status == event.TaskWaitingApproval:
		e.transition(ctx, t, event.TaskRunning, "", "", event.ActorSystem, keepNext(t))
	}
}

// pauseChanged notes on the task that its run is paused, or no longer.
func (e *Engine) pauseChanged(ctx context.Context, ev event.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store().Task(ctx, ev.TaskID)
	if err != nil || (t.Status != event.TaskRunning && t.Status != event.TaskWaitingApproval) {
		return
	}
	if ev.Type == event.RunPaused {
		var p event.RunPausePayload
		ev.Decode(&p)
		// Its open approvals expired with the pause.
		e.transition(ctx, t, event.TaskRunning, PausedPrefix+firstNonEmpty(p.Reason, "已暂停"), "", event.ActorSystem, keepNext(t))
	} else if strings.HasPrefix(t.StatusReason, PausedPrefix) {
		e.transition(ctx, t, t.Status, "", "", event.ActorSystem, keepNext(t))
	}
}

// PausedPrefix starts the status reason of a task whose run is paused.
const PausedPrefix = "已暂停："

// transition appends a task.status event unless nothing changes.
func (e *Engine) transition(ctx context.Context, t store.Task, to, reason, prURL, actor string, opts ...func(*event.TaskStatusPayload)) error {
	p := event.TaskStatusPayload{From: t.Status, To: to, Reason: reason, PRURL: prURL}
	for _, o := range opts {
		o(&p)
	}
	if t.Status == to && t.StatusReason == reason && prURL == "" && t.Next == p.Next {
		return nil
	}
	ev := event.New(event.TaskStatus, actor, t.OriginThreadID, p)
	ev.TaskID = t.ID
	_, err := e.Log.Append(ctx, ev)
	return err
}

// keepNext keeps what the task's run is for (work, rebase, deliver) across
// changes that do not start a new run, so the workbench can show the stage.
func keepNext(t store.Task) func(*event.TaskStatusPayload) {
	return func(p *event.TaskStatusPayload) { p.Next = t.Next }
}

func waiting(what, next string) func(*event.TaskStatusPayload) {
	return func(p *event.TaskStatusPayload) { p.Waiting, p.Next = what, next }
}

// Input creates a task.
type Input struct {
	ThreadID  string
	ParentID  string
	Kind      string // code / general; empty: code when a project is given
	Title     string
	Detail    string
	Priority  int
	ProjectID string
	Agent     string
	Node      string
	Mode      string // default / auto / open; empty: the project's or the global default
	Persona   string // persona id; empty: the project's default persona, if any
	DependsOn []string
	Dispatch  bool // start right away (or queue)
	Actor     string
}

// Create validates and stores a new task, and dispatches it when asked.
func (e *Engine) Create(ctx context.Context, in Input) (store.Task, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return store.Task{}, fmt.Errorf("%w: 任务需要标题", ErrInvalid)
	}
	if in.Kind == "" {
		in.Kind = event.TaskKindGeneral
		if in.ProjectID != "" && in.ProjectID != projectkey.MiscID {
			in.Kind = event.TaskKindCode
		}
	}
	if in.Kind != event.TaskKindCode && in.Kind != event.TaskKindGeneral && in.Kind != event.TaskKindOps {
		return store.Task{}, fmt.Errorf("%w: kind 只能是 code、general 或 ops", ErrInvalid)
	}
	if in.Kind == event.TaskKindOps && (in.ProjectID == "" || in.ProjectID == projectkey.MiscID) {
		return store.Task{}, fmt.Errorf("%w: 运维任务需要指定项目", ErrInvalid)
	}
	if in.ProjectID == "" {
		if _, err := e.store().Project(ctx, projectkey.MiscID); err == nil {
			in.ProjectID = projectkey.MiscID // every task belongs to a project
		}
	}
	if in.Persona != "" {
		if _, err := e.store().Persona(ctx, in.Persona); err != nil {
			return store.Task{}, fmt.Errorf("%w: 岗位 %s 不存在", ErrInvalid, in.Persona)
		}
	}
	if err := validMode(in.Mode); err != nil {
		return store.Task{}, err
	}
	if in.Kind == event.TaskKindCode && in.ProjectID == "" {
		return store.Task{}, fmt.Errorf("%w: 代码任务需要指定项目", ErrInvalid)
	}
	var archived bool
	if in.ProjectID != "" {
		p, err := e.store().Project(ctx, in.ProjectID)
		if err != nil {
			return store.Task{}, fmt.Errorf("%w: 项目 %s 不存在", ErrInvalid, in.ProjectID)
		}
		archived = p.Archived()
	}
	if in.ParentID != "" {
		if _, err := e.store().Task(ctx, in.ParentID); err != nil {
			return store.Task{}, fmt.Errorf("%w: 父任务 %s 不存在", ErrInvalid, in.ParentID)
		}
	}
	for _, d := range in.DependsOn {
		if _, err := e.store().Task(ctx, d); err != nil {
			return store.Task{}, fmt.Errorf("%w: 依赖的任务 %s 不存在", ErrInvalid, d)
		}
	}
	if in.Actor == "" {
		in.Actor = event.ActorUser
	}
	status := event.TaskPlanned
	if in.Actor == event.ActorUser && !in.Dispatch {
		status = event.TaskInbox
	}
	ev := event.New(event.TaskCreated, in.Actor, in.ThreadID, event.TaskCreatedPayload{
		ParentID: in.ParentID, Kind: in.Kind, Title: in.Title, Detail: strings.TrimSpace(in.Detail), Priority: in.Priority,
		ProjectID: in.ProjectID, AgentPref: in.Agent, NodePref: in.Node, PermissionMode: in.Mode, PersonaID: in.Persona, DependsOn: in.DependsOn, Status: status,
	})
	ev.TaskID = ids.New()
	if archived {
		// New work in an archived project brings it back to the workbench.
		if _, err := e.Log.Append(ctx, event.New(event.ProjectArchived, in.Actor, "",
			event.ProjectArchivedPayload{ProjectID: in.ProjectID, Archived: false})); err != nil {
			return store.Task{}, err
		}
	}
	if _, err := e.Log.Append(ctx, ev); err != nil {
		return store.Task{}, err
	}
	if in.Dispatch {
		return e.Dispatch(ctx, ev.TaskID, "", "", in.Actor)
	}
	return e.store().Task(ctx, ev.TaskID)
}

func validMode(m string) error {
	switch m {
	case "", proto.ModeDefault, proto.ModeAuto, proto.ModeOpen:
		return nil
	}
	return fmt.Errorf("%w: 权限模式只能是 default、auto 或 open", ErrInvalid)
}

// Update changes the editable fields of a task.
func (e *Engine) Update(ctx context.Context, id string, f event.TaskFields, actor string) (store.Task, error) {
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return store.Task{}, err
	}
	if f.Title != nil {
		if *f.Title = strings.TrimSpace(*f.Title); *f.Title == "" {
			return store.Task{}, fmt.Errorf("%w: 标题不能为空", ErrInvalid)
		}
	}
	if f.PermissionMode != nil {
		if err := validMode(*f.PermissionMode); err != nil {
			return store.Task{}, err
		}
	}
	if f.ProjectID != nil && *f.ProjectID != "" {
		if _, err := e.store().Project(ctx, *f.ProjectID); err != nil {
			return store.Task{}, fmt.Errorf("%w: 项目 %s 不存在", ErrInvalid, *f.ProjectID)
		}
	}
	if f.PersonaID != nil && *f.PersonaID != "" {
		if _, err := e.store().Persona(ctx, *f.PersonaID); err != nil {
			return store.Task{}, fmt.Errorf("%w: 岗位 %s 不存在", ErrInvalid, *f.PersonaID)
		}
	}
	if f.SetDeps || len(f.DependsOn) > 0 {
		for _, d := range f.DependsOn {
			if _, err := e.store().Task(ctx, d); err != nil {
				return store.Task{}, fmt.Errorf("%w: 依赖的任务 %s 不存在", ErrInvalid, d)
			}
			if e.dependsOn(ctx, d, id, map[string]bool{}) {
				return store.Task{}, fmt.Errorf("%w: 依赖关系成环了（%s 已经依赖这个任务）", ErrInvalid, d)
			}
		}
	}
	ev := event.New(event.TaskUpdated, actor, t.OriginThreadID, event.TaskUpdatedPayload{TaskFields: f})
	ev.TaskID = id
	if _, err := e.Log.Append(ctx, ev); err != nil {
		return store.Task{}, err
	}
	// The agent at work only reads its prompt once: tell it what changed.
	if f.Detail != nil && *f.Detail != t.Detail {
		if r, err := e.activeRun(ctx, id); err == nil && r.Status == event.RunStatusRunning && r.Purpose != event.PurposeDeliver {
			if err := e.Nodes.Message(ctx, r.ID, "任务说明更新了，请按新的说明继续：\n\n"+*f.Detail); err != nil {
				slog.Warn("task: forward the new detail", "task", id, "err", err)
			}
		}
	}
	return e.store().Task(ctx, id)
}

// dependsOn reports whether task a depends on b, directly or not.
func (e *Engine) dependsOn(ctx context.Context, a, b string, seen map[string]bool) bool {
	if a == b {
		return true
	}
	if seen[a] {
		return false
	}
	seen[a] = true
	t, err := e.store().Task(ctx, a)
	if err != nil {
		return false
	}
	for _, d := range t.DependsOn {
		if e.dependsOn(ctx, d, b, seen) {
			return true
		}
	}
	return false
}

// SetStatus is a manual status change by the user or the supervisor. The
// engine moves tasks through running, review and delivery by itself; by hand
// a task can only be filed, marked blocked or done, or reopened.
func (e *Engine) SetStatus(ctx context.Context, id, to, reason, actor string) (store.Task, error) {
	if to == event.TaskCancelled {
		if err := e.Cancel(ctx, id, reason, actor); err != nil {
			return store.Task{}, err
		}
		return e.store().Task(ctx, id)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return store.Task{}, err
	}
	switch to {
	case event.TaskInbox, event.TaskPlanned, event.TaskBlocked, event.TaskDone:
	default:
		return store.Task{}, fmt.Errorf("%w: 不能手动把任务改成 %s；要执行请用派发", ErrConflict, to)
	}
	if active(t.Status) {
		return store.Task{}, fmt.Errorf("%w: 任务正在执行，先取消它", ErrConflict)
	}
	if err := e.transition(ctx, t, to, reason, "", actor); err != nil {
		return store.Task{}, err
	}
	return e.store().Task(ctx, id)
}

// active reports whether a task has a run going (or one waiting for a slot).
func active(status string) bool {
	switch status {
	case event.TaskRunning, event.TaskWaitingApproval, event.TaskReview, event.TaskQueued:
		return true
	}
	return false
}

func final(status string) bool {
	return status == event.TaskDone || status == event.TaskCancelled
}

// Cancel stops a task's runs and its open subtasks.
func (e *Engine) Cancel(ctx context.Context, id, reason, actor string) error {
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return err
	}
	if final(t.Status) {
		return fmt.Errorf("%w: 任务已经%s", ErrConflict, statusName[t.Status])
	}
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: id, Statuses: []string{event.RunStatusQueued, event.RunStatusRunning}})
	for _, r := range runs {
		if err := e.Nodes.Cancel(ctx, r.ID, "任务已取消"); err != nil {
			slog.Warn("task: cancel run", "run", r.ID, "err", err)
		}
	}
	if reason == "" {
		reason = "已取消"
	}
	if err := e.transition(ctx, t, event.TaskCancelled, reason, "", actor); err != nil {
		return err
	}
	children, _ := e.store().Tasks(ctx, store.TaskFilter{ParentID: id})
	for _, c := range children {
		if !final(c.Status) {
			e.Cancel(ctx, c.ID, "父任务已取消", actor)
		}
	}
	e.cleanup(ctx, t)
	return nil
}

// Dispatch asks for a task to run, optionally on a given machine or with a
// given agent (remembered on the task). It runs now when it can, or waits in
// the queue for its dependencies or a machine.
func (e *Engine) Dispatch(ctx context.Context, id, node, agent, actor string) (store.Task, error) {
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return store.Task{}, err
	}
	if active(t.Status) && t.Status != event.TaskQueued {
		return store.Task{}, fmt.Errorf("%w: 任务正在执行", ErrConflict)
	}
	if t.Status == event.TaskDone || t.Status == event.TaskCancelled {
		// Reopening a closed task is allowed: it gets a fresh round of work.
		if t.Status == event.TaskDone && t.PRURL != "" {
			return store.Task{}, fmt.Errorf("%w: 任务已经交付了 PR，请新建一个任务", ErrConflict)
		}
	}
	if node != "" || agent != "" {
		f := event.TaskFields{}
		if node != "" {
			f.NodePref = &node
		}
		if agent != "" {
			f.AgentPref = &agent
		}
		if t, err = e.Update(ctx, id, f, actor); err != nil {
			return store.Task{}, err
		}
	}
	e.mu.Lock()
	next := event.PurposeWork
	if t.Status == event.TaskQueued && t.Next != "" {
		next = t.Next
	} else if last := e.lastRun(ctx, id, event.PurposeWork, event.PurposeRebase, event.PurposeDeliver); last.Purpose == event.PurposeDeliver {
		next = event.PurposeDeliver // the work passed review; only the delivery is left
	}
	err = e.transition(ctx, t, event.TaskQueued, "等待派发", "", actor, waiting("", next))
	e.mu.Unlock()
	if err != nil {
		return store.Task{}, err
	}
	if _, err := e.tryStart(ctx, id); err != nil {
		return store.Task{}, err
	}
	return e.store().Task(ctx, id)
}

// tryStart starts a queued task's next run if its dependencies are done and
// a machine is free; otherwise it records what the task waits for.
func (e *Engine) tryStart(ctx context.Context, id string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return false, err
	}
	if t.Status != event.TaskQueued {
		return false, nil
	}
	next := t.Next
	if next == "" {
		next = event.PurposeWork
	}
	for _, d := range t.DependsOn {
		dep, err := e.store().Task(ctx, d)
		if err != nil {
			continue
		}
		switch dep.Status {
		case event.TaskDone:
			continue
		case event.TaskCancelled, event.TaskFailed:
			return false, e.transition(ctx, t, event.TaskBlocked, fmt.Sprintf("依赖的任务「%s」%s", dep.Title, statusName[dep.Status]), "", event.ActorSystem)
		}
		return false, e.transition(ctx, t, event.TaskQueued, fmt.Sprintf("等待任务「%s」完成", dep.Title), "", event.ActorSystem, waiting(event.WaitingDeps, next))
	}

	choice, err := e.route(ctx, t, next)
	if errors.Is(err, router.ErrNoMachine) {
		reason := "等待机器：" + strings.TrimPrefix(err.Error(), router.ErrNoMachine.Error()+": ")
		first := !strings.HasPrefix(t.StatusReason, "等待机器")
		return false, e.transition(ctx, t, event.TaskQueued, reason, "", event.ActorSystem, waiting(event.WaitingMachine, next),
			func(p *event.TaskStatusPayload) { p.Notify = first })
	} else if err != nil {
		return false, err
	}
	if err := e.startRun(ctx, t, next, choice); err != nil {
		return false, err
	}
	return true, nil
}

// route picks the machine for the task's next run. Later runs of a code task
// stay on the machine that holds its branch.
func (e *Engine) route(ctx context.Context, t store.Task, purpose string) (router.Choice, error) {
	st := e.store()
	var proj store.Project
	if t.ProjectID != "" {
		p, err := st.Project(ctx, t.ProjectID)
		if err != nil {
			return router.Choice{}, fmt.Errorf("%w: 项目已被删除", router.ErrNoMachine)
		}
		proj = p
	}
	req := router.Request{Kind: t.Kind, ProjectNodes: proj.Nodes, NodePref: t.NodePref, Role: "dev"}
	persona := e.personaOf(ctx, t, proj)
	req.Agent = firstNonEmpty(t.AgentPref, persona.Agent, proj.DefaultAgent, e.setting(ctx, SettingDefaultAgent))
	if t.Kind == event.TaskKindOps {
		req.Role = "server"
		if d := proj.Deploy; d != nil && d.NodeID != "" && (t.NodePref == "" || t.NodePref == d.NodeID) {
			req.NodePref, req.Path = d.NodeID, d.Path
		}
	}
	last := e.lastRun(ctx, t.ID, event.PurposeWork, event.PurposeRebase)
	if t.Kind == event.TaskKindCode && t.Branch != "" && last.NodeID != "" {
		req.Sticky = last.NodeID
	}
	if purpose != event.PurposeWork && last.Agent != "" && t.AgentPref == "" {
		req.Agent = last.Agent // a rebase goes to the agent that wrote the code
	}

	nodes, err := st.Nodes(ctx)
	if err != nil {
		return router.Choice{}, err
	}
	var rn []router.Node
	for _, n := range nodes {
		rn = append(rn, router.Node{
			ID: n.ID, Name: n.Name, Online: e.Nodes.Online(n.ID), Agents: parseAgents(n.Agents),
			Running: e.Nodes.Busy(ctx, n.ID), Capacity: n.Concurrency, Role: n.Role,
		})
	}
	if purpose == event.PurposeDeliver {
		// Delivery needs no agent: it runs on the machine with the branch.
		for i := range rn {
			if rn[i].ID == req.Sticky {
				rn[i].Agents = append(rn[i].Agents, proto.AgentInfo{Name: "sous", Installed: true})
			}
		}
		req.Agent = "sous"
	}
	return router.Route(req, rn)
}

// startRun creates the run and marks the task running.
func (e *Engine) startRun(ctx context.Context, t store.Task, purpose string, c router.Choice) error {
	proj, _ := e.store().Project(ctx, t.ProjectID)
	persona := e.personaOf(ctx, t, proj)
	mode := firstNonEmpty(t.PermissionMode, persona.DefaultMode, proj.DefaultMode, e.setting(ctx, SettingDefaultMode), proto.ModeDefault)
	thread := t.OriginThreadID
	if thread == "" {
		thread, _ = e.store().MainThreadID(ctx)
	}
	cr := nodehub.CreateRun{
		ThreadID: thread, NodeID: c.NodeID, Agent: c.Agent, RepoPath: c.Path, TaskID: t.ID, Purpose: purpose,
		PermissionMode: mode, Branch: t.Branch, BaseBranch: t.BaseBranch, Title: t.Title,
		Persona: persona.Name,
	}
	if purpose == event.PurposeWork || purpose == event.PurposeRebase {
		cr.Options = persona.Options
	}
	if t.Kind == event.TaskKindOps {
		cr.Kind = event.RunKindOps
	}
	reason := ""
	switch purpose {
	case event.PurposeDeliver:
		cr.Prompt = t.Detail
		cr.Force = e.countRuns(ctx, t.ID, event.PurposeRebase) > 0
		reason = "正在交付：push 分支并创建 PR"
	case event.PurposeRebase:
		cr.Prompt = rebasePrompt(t)
		reason = "PR 有冲突，正在 rebase"
	default:
		cr.Prompt = e.workPrompt(ctx, t, proj, persona, c.Path != "" && t.Kind != event.TaskKindOps)
	}
	if prev := e.lastRun(ctx, t.ID, event.PurposeWork, event.PurposeRebase, event.PurposeDeliver); prev.Status == event.RunStatusInterrupted && purpose != event.PurposeDeliver {
		cr.RecoveryNote = e.recoveryNote(ctx, prev)
	} else if prev.Status == event.RunStatusInterrupted {
		cr.RecoveryNote = "（交付在中断后重新开始）" // marks the run as a recovery
	}
	if _, err := e.Nodes.Create(ctx, cr); err != nil {
		return err
	}
	// While running, next records what the run is for: the stage it is in.
	return e.transition(ctx, t, event.TaskRunning, reason, "", event.ActorSystem, waiting("", purpose))
}

func (e *Engine) workPrompt(ctx context.Context, t store.Task, proj store.Project, persona store.Persona, inRepo bool) string {
	var b strings.Builder
	if brief := strings.TrimSpace(persona.Brief); brief != "" {
		fmt.Fprintf(&b, "【你的岗位：%s】%s\n\n", persona.Name, brief)
	}
	fmt.Fprintf(&b, "任务：%s\n", t.Title)
	if t.Detail != "" {
		b.WriteString("\n" + t.Detail + "\n")
	}
	if proj.Name != "" && proj.ID != projectkey.MiscID {
		fmt.Fprintf(&b, "\n项目：%s\n", proj.Name)
		if notes := strings.TrimSpace(proj.Notes); notes != "" {
			b.WriteString("项目说明：" + notes + "\n")
		}
	}
	if inRepo {
		// The agent starts inside the task's own worktree. Without this it
		// goes looking for the project elsewhere (or for apps/<name>).
		b.WriteString("\n工作目录：当前目录就是项目的代码根目录，是 sous 为这个任务建好的独立 git worktree，已经在任务分支上。" +
			"直接在当前目录里读写文件，不要去别的路径找项目，不要切换分支，不要改动原仓库。\n" +
			"不要 push，也不要创建 PR：提交即可（或留着未提交的改动），sous 会在验收通过后自己 push 并开 PR。\n")
	}
	if t.Kind == event.TaskKindOps {
		b.WriteString(opsPrompt(proj))
	}
	if n := e.countRuns(ctx, t.ID, event.PurposeWork); n > 0 && t.ReviewNote != "" {
		fmt.Fprintf(&b, "\n这是第 %d 次尝试。上一次的结果没有通过验收，原因：%s\n"+
			"工作目录里保留着之前的改动；先看一下 git status 和 git log，在此基础上修改。\n", n+1, t.ReviewNote)
	}
	b.WriteString("\n完成后用几句话总结你改了什么、怎么验证的。")
	return b.String()
}

func rebasePrompt(t store.Task) string {
	base := t.BaseBranch
	if base == "" {
		base = "main"
	}
	return fmt.Sprintf("任务「%s」的分支 %s 已经创建了 PR，但和目标分支 %s 有冲突。\n"+
		"请先 git fetch origin，再把当前分支 rebase 到 origin/%s 上，解决所有冲突，确认能构建、测试通过，然后提交。\n"+
		"不要 push，也不要切换分支；sous 会在你完成后自己 push。", t.Title, t.Branch, base, base)
}

func (e *Engine) lastRun(ctx context.Context, taskID string, purposes ...string) store.Run {
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: taskID})
	for i := len(runs) - 1; i >= 0; i-- {
		for _, p := range purposes {
			if runs[i].Purpose == p {
				return runs[i]
			}
		}
	}
	return store.Run{}
}

func (e *Engine) countRuns(ctx context.Context, taskID, purpose string) int {
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: taskID})
	n := 0
	for _, r := range runs {
		if r.Purpose == purpose {
			n++
		}
	}
	return n
}

func (e *Engine) setting(ctx context.Context, key string) string {
	v, _ := e.store().Setting(ctx, key)
	return v
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

var statusName = map[string]string{
	event.TaskInbox: "待整理", event.TaskPlanned: "已计划", event.TaskQueued: "排队中", event.TaskRunning: "进行中",
	event.TaskWaitingApproval: "等待审批", event.TaskBlocked: "受阻", event.TaskReview: "验收中",
	event.TaskDone: "已完成", event.TaskFailed: "失败了", event.TaskCancelled: "已取消",
}

// personaOf is the persona that does a task: its own, else the project's
// default. The zero Persona when there is none.
func (e *Engine) personaOf(ctx context.Context, t store.Task, proj store.Project) store.Persona {
	for _, id := range []string{t.PersonaID, proj.DefaultPersona} {
		if id == "" {
			continue
		}
		if p, err := e.store().Persona(ctx, id); err == nil {
			return p
		}
	}
	return store.Persona{}
}

// opsPrompt tells an operations run where it is and how to deploy.
func opsPrompt(proj store.Project) string {
	var b strings.Builder
	b.WriteString("\n这是一个运维任务：你直接在服务器上项目的目录里操作，没有 worktree，也不会创建 PR。\n")
	if d := proj.Deploy; d != nil && d.Command != "" {
		fmt.Fprintf(&b, "部署方式：在 %s 里执行 `%s`。按这个脚本或命令执行，不要临时修改服务器上的文件；失败就停下来说明原因。\n", d.Path, d.Command)
		if d.Health != "" {
			fmt.Fprintf(&b, "完成后执行健康检查 `%s`，把结果写进总结。\n", d.Health)
		}
	} else {
		b.WriteString("项目没有登记部署命令：先看仓库里的部署脚本或说明（README、AGENTS.md、work/infra/），按它执行；找不到就停下来说明。\n")
	}
	b.WriteString("重启服务、删除文件这类操作会请用户批准。\n")
	return b.String()
}

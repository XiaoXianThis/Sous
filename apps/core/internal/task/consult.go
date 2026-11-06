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
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/router"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// A project's consultant (decision 0022): the supervisor discusses a
// project with the user and asks a read-only agent on a machine that has
// the project. The consultant keeps one session per project: between
// questions it waits; after a while its agent stops, and the next question
// loads the session again. Its answers go into the conversation as they are
// (kind "consult"); the supervisor does not repeat them. When the discussion
// turns into a task, the session goes with it (Handoff).

// ConsultWait is how long a question waits for the consultant's answer
// before the answer is left to arrive in the conversation by itself.
var ConsultWait = 10 * time.Minute

// consultAsk is where a consultant's next answer goes.
type consultAsk struct {
	ThreadID string
	ReplyTo  int64
}

// ConsultInput is one question for a project's consultant.
type ConsultInput struct {
	ProjectID string
	Question  string
	NewTopic  bool // start a fresh session instead of going on
	NodeID    string
	ThreadID  string
	ReplyTo   int64
}

// ConsultAnswer is what the consultant said.
type ConsultAnswer struct {
	Text    string
	RunID   string
	Agent   string
	NodeID  string
	Pending bool // no answer within the wait; it will appear in the conversation
}

// consultLock serializes the questions to one project's consultant: two
// questions in the same turn (tool calls run in parallel) go to the same
// session one after the other, instead of opening two sessions.
func (e *Engine) consultLock(projectID string) func() {
	e.wmu.Lock()
	mu, ok := e.consultMu[projectID]
	if !ok {
		mu = &sync.Mutex{}
		e.consultMu[projectID] = mu
	}
	e.wmu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// Consult asks a project's consultant and waits for the answer.
func (e *Engine) Consult(ctx context.Context, in ConsultInput) (ConsultAnswer, error) {
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" {
		return ConsultAnswer{}, fmt.Errorf("%w: 问题不能为空", ErrInvalid)
	}
	p, err := e.store().Project(ctx, in.ProjectID)
	if err != nil {
		return ConsultAnswer{}, fmt.Errorf("%w: 没有项目 %s", ErrInvalid, in.ProjectID)
	}
	if len(p.Nodes) == 0 {
		return ConsultAnswer{}, fmt.Errorf("%w: 项目 %s 还不在任何机器上，顾问没有代码可看", ErrConflict, p.Name)
	}
	if in.ThreadID == "" {
		in.ThreadID, _ = e.store().MainThreadID(ctx)
	}
	defer e.consultLock(p.ID)()
	cur, err := e.store().Consultant(ctx, p.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return ConsultAnswer{}, err
	}
	has := err == nil
	if has && (in.NewTopic || (in.NodeID != "" && cur.NodeID != in.NodeID)) {
		e.closeConsultant(ctx, p.ID, cur, "换了一个话题")
		has = false
	}
	ask := consultAsk{ThreadID: in.ThreadID, ReplyTo: in.ReplyTo}
	ch := make(chan event.Event, 8)
	var runID string
	if has && (cur.Status == event.RunStatusRunning || cur.Status == event.RunStatusQueued) {
		runID = cur.ID
		e.expect(runID, ask, ch)
		if err := e.Nodes.MessageAs(ctx, runID, in.Question, event.ActorSupervisor); err != nil {
			e.unexpect(runID)
			return ConsultAnswer{}, err
		}
	} else {
		cr, err := e.consultRun(ctx, p, cur, has, in)
		if err != nil {
			return ConsultAnswer{}, err
		}
		runID = ids.New()
		cr.RunID = runID
		e.expect(runID, ask, ch)
		if _, err := e.Nodes.Create(ctx, cr); err != nil {
			e.unexpect(runID)
			return ConsultAnswer{}, err
		}
	}
	defer e.unwait(runID)
	t := time.NewTimer(ConsultWait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ConsultAnswer{RunID: runID, Pending: true}, nil
		case <-t.C:
			return ConsultAnswer{RunID: runID, Pending: true}, nil
		case ev := <-ch:
			r, _ := e.store().Run(ctx, runID)
			switch ev.Type {
			case event.RunTurn:
				var tp event.RunTurnPayload
				ev.Decode(&tp)
				e.postAnswer(ctx, r, p, ask, tp.Text)
				return ConsultAnswer{Text: tp.Text, RunID: runID, Agent: r.Agent, NodeID: r.NodeID}, nil
			case event.RunEnded:
				e.dropAsk(runID)
				return ConsultAnswer{}, fmt.Errorf("%w: 顾问没有回答就停了：%s", ErrConflict, firstNonEmpty(r.Error, r.Summary, r.Status))
			}
		}
	}
}

// consultRun prepares a new consultant run: it goes on in the earlier
// session when there is one to load, else starts fresh on a machine with
// the project.
func (e *Engine) consultRun(ctx context.Context, p store.Project, cur store.Run, has bool, in ConsultInput) (nodehub.CreateRun, error) {
	cr := nodehub.CreateRun{ThreadID: in.ThreadID, Purpose: event.PurposeConsult, Kind: proto.KindInPlace, ProjectID: p.ID,
		PermissionMode: proto.ModeReadOnly, Title: "顾问：" + p.Name}
	resumable := has && cur.Session != "" && e.Nodes.Online(cur.NodeID)
	if resumable {
		cr.NodeID, cr.Agent, cr.RepoPath, cr.ResumeSession = cur.NodeID, cur.Agent, cur.RepoPath, cur.Session
		cr.Prompt = in.Question
		return cr, nil
	}
	nodes, err := e.store().Nodes(ctx)
	if err != nil {
		return cr, err
	}
	var rn []router.Node
	for _, n := range nodes {
		rn = append(rn, router.Node{ID: n.ID, Name: n.Name, Online: e.Nodes.Online(n.ID), Agents: parseAgents(n.Agents), Role: n.Role})
	}
	agent := firstNonEmpty(p.DefaultAgent, e.setting(ctx, SettingDefaultAgent))
	if has {
		agent = firstNonEmpty(cur.Agent, agent)
	}
	c, err := router.Route(router.Request{Place: event.PlaceInPlace, ProjectNodes: p.Nodes, NodePref: in.NodeID, Agent: agent, Prefer: proto.RoleDev}, rn)
	if err != nil {
		return cr, fmt.Errorf("%w: 找不到能看这个项目的机器：%s", ErrConflict, strings.TrimPrefix(err.Error(), router.ErrNoMachine.Error()+": "))
	}
	cr.NodeID, cr.Agent, cr.RepoPath = c.NodeID, c.Agent, c.Path
	var b strings.Builder
	b.WriteString(consultPrompt(p))
	if has {
		// The earlier session cannot be loaded: carry over what was said.
		if turns, _ := e.store().ConsultTurns(ctx, p.ID, 3); len(turns) > 0 {
			b.WriteString("\n\n之前的讨论里你说过（会话没法恢复，这是摘录）：\n")
			for _, t := range turns {
				b.WriteString("---\n" + truncate(t, 1500) + "\n")
			}
		}
	}
	b.WriteString("\n\n问题：" + in.Question)
	cr.Prompt = b.String()
	return cr, nil
}

func consultPrompt(p store.Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是项目「%s」的顾问。用户正在和 sous（主管）讨论这个项目的需求和方案，sous 会把问题转给你，你的回答会原样给用户看。\n", p.Name)
	b.WriteString("当前目录就是项目的目录。你只能读文件、搜索、看 git 记录，不能改任何东西，也不能执行有副作用的命令；不要开始实现。\n" +
		"回答要具体：指出相关的文件和代码位置，说明现状、可选的做法和各自的取舍；不确定就说不确定，需要用户决定的地方说清楚。简洁，不要寒暄。")
	if notes := strings.TrimSpace(p.Notes); notes != "" {
		b.WriteString("\n项目说明：" + notes)
	}
	return b.String()
}

// expect registers where a consultant's next answer goes and who waits.
func (e *Engine) expect(runID string, ask consultAsk, ch chan event.Event) {
	e.wmu.Lock()
	e.asks[runID] = ask
	e.waiters[runID] = ch
	e.wmu.Unlock()
}

func (e *Engine) unexpect(runID string) {
	e.wmu.Lock()
	delete(e.asks, runID)
	delete(e.waiters, runID)
	e.wmu.Unlock()
}

func (e *Engine) dropAsk(runID string) {
	e.wmu.Lock()
	delete(e.asks, runID)
	e.wmu.Unlock()
}

// postAnswer puts a consultant's answer into the conversation as it is.
func (e *Engine) postAnswer(ctx context.Context, r store.Run, p store.Project, ask consultAsk, text string) {
	e.dropAsk(r.ID)
	thread := firstNonEmpty(ask.ThreadID, r.ThreadID)
	ev := event.New(event.MessageAssistant, event.ActorSystem, thread, event.MessagePayload{
		MessageID: ids.New(), Content: text, Kind: event.KindConsult, ReplyTo: ask.ReplyTo,
		Source: &event.MessageSource{RunID: r.ID, Agent: r.Agent, ProjectID: p.ID, Project: p.Name, NodeID: r.NodeID},
	})
	ev.RunID = r.ID
	if _, err := e.Log.Append(ctx, ev); err != nil {
		slog.Error("task: post a consultant's answer", "run", r.ID, "err", err)
	}
}

// lateAnswer posts an answer that came after the question stopped waiting.
func (e *Engine) lateAnswer(ev event.Event) {
	ctx := context.Background()
	r, err := e.store().Run(ctx, ev.RunID)
	if err != nil || r.Purpose != event.PurposeConsult {
		return
	}
	e.wmu.Lock()
	ask, ok := e.asks[ev.RunID]
	e.wmu.Unlock()
	if !ok {
		ask = consultAsk{ThreadID: r.ThreadID}
	}
	p, _ := e.store().Project(ctx, r.ProjectID)
	var tp event.RunTurnPayload
	ev.Decode(&tp)
	e.postAnswer(ctx, r, p, ask, tp.Text)
}

// closeConsultant retires a project's consultant session.
func (e *Engine) closeConsultant(ctx context.Context, projectID string, cur store.Run, reason string) {
	if cur.Status == event.RunStatusRunning || cur.Status == event.RunStatusQueued {
		if err := e.Nodes.Cancel(ctx, cur.ID, reason); err != nil {
			slog.Warn("task: stop a consultant", "run", cur.ID, "err", err)
		}
	}
	ev := event.New(event.ConsultClosed, event.ActorSupervisor, "", event.ConsultClosedPayload{ProjectID: projectID, Reason: reason})
	if _, err := e.Log.Append(ctx, ev); err != nil {
		slog.Error("task: close a consultant", "project", projectID, "err", err)
	}
}

// takeConsultant hands a project's consultant session to a new task: the
// agent that did the reading goes on with the work.
func (e *Engine) takeConsultant(ctx context.Context, projectID string) (*event.Handoff, error) {
	if projectID == "" {
		return nil, fmt.Errorf("%w: 要从讨论转成任务，得指定项目", ErrInvalid)
	}
	cur, err := e.store().Consultant(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: 这个项目现在没有和顾问的讨论；先用 consult 讨论，或者直接建任务", ErrConflict)
	} else if err != nil {
		return nil, err
	}
	h := &event.Handoff{NodeID: cur.NodeID, Agent: cur.Agent, SessionID: cur.Session}
	if turns, _ := e.store().ConsultTurns(ctx, projectID, 4); len(turns) > 0 {
		var parts []string
		for _, t := range turns {
			parts = append(parts, truncate(t, 1500))
		}
		h.Note = strings.Join(parts, "\n---\n")
	}
	e.closeConsultant(ctx, projectID, cur, "讨论转成了任务")
	return h, nil
}

// ConsultState describes a project's consultant for the supervisor and the
// page: "" when there is none.
func (e *Engine) ConsultState(ctx context.Context, projectID string) string {
	cur, err := e.store().Consultant(ctx, projectID)
	if err != nil {
		return ""
	}
	switch {
	case cur.Status == event.RunStatusRunning || cur.Status == event.RunStatusQueued:
		return fmt.Sprintf("有一个进行中的顾问讨论（%s，在 %s 上）", cur.Agent, e.nodeName(ctx, cur.NodeID))
	case cur.Session != "":
		return fmt.Sprintf("有一个暂停的顾问讨论（%s，下次提问会接上会话）", cur.Agent)
	}
	return ""
}

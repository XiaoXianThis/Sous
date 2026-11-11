package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// DMTarget names who the user is talking to directly: one of a running run,
// a task's agent, or the project's consultant (decision 0028).
type DMTarget struct {
	RunID   string `json:"run_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Consult bool   `json:"consult,omitempty"`
}

// DMInput is one direct message inside a project chat.
type DMInput struct {
	ThreadID string
	TopicID  string
	Content  string
	To       DMTarget
}

const dmMaxRunes = 20000

// DM delivers what the user says to an agent directly, without the
// supervisor in between. It returns the stored user message's id.
func (e *Engine) DM(ctx context.Context, in DMInput) (string, error) {
	thread, project, err := e.dmPlace(ctx, in.ThreadID)
	if err != nil {
		return "", err
	}
	n := 0
	if in.To.RunID != "" {
		n++
	}
	if in.To.TaskID != "" {
		n++
	}
	if in.To.Consult {
		n++
	}
	if n != 1 {
		return "", fmt.Errorf("%w: 要指定一个对象", ErrInvalid)
	}
	in.Content = strings.TrimSpace(in.Content)
	if in.Content == "" {
		return "", fmt.Errorf("%w: 消息不能为空", ErrInvalid)
	}
	if utf8.RuneCountInString(in.Content) > dmMaxRunes {
		return "", fmt.Errorf("%w: 消息太长", ErrInvalid)
	}
	_ = thread

	switch {
	case in.To.Consult:
		return e.dmConsult(ctx, project, in)
	case in.To.RunID != "":
		return e.dmToRun(ctx, project, in)
	default:
		return e.dmToTask(ctx, project, in)
	}
}

func (e *Engine) dmPlace(ctx context.Context, threadID string) (store.Thread, store.Project, error) {
	thread, err := e.store().Thread(ctx, threadID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Thread{}, store.Project{}, fmt.Errorf("%w: 对话不存在", ErrInvalid)
	}
	if err != nil {
		return store.Thread{}, store.Project{}, err
	}
	if thread.Kind != "project" || thread.ProjectID == "" {
		return store.Thread{}, store.Project{}, fmt.Errorf("%w: 私聊只能在项目对话里用", ErrInvalid)
	}
	project, err := e.store().Project(ctx, thread.ProjectID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Thread{}, store.Project{}, fmt.Errorf("%w: 项目不存在", ErrInvalid)
	}
	if err != nil {
		return store.Thread{}, store.Project{}, err
	}
	return thread, project, nil
}

// dmRunInProject checks a run belongs to the project and is still going.
func (e *Engine) dmRunInProject(ctx context.Context, id string, project store.Project) (store.Run, error) {
	r, err := e.store().Run(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Run{}, fmt.Errorf("%w: 这个 Run 不存在", ErrInvalid)
	}
	if err != nil {
		return store.Run{}, err
	}
	ok := r.ProjectID == project.ID
	if r.TaskID != "" {
		if t, err := e.store().Task(ctx, r.TaskID); err == nil && t.ProjectID == project.ID {
			ok = true
		}
	}
	if !ok {
		return store.Run{}, fmt.Errorf("%w: 这个 Run 不在这个项目里", ErrInvalid)
	}
	if r.Status != event.RunStatusRunning {
		return store.Run{}, fmt.Errorf("%w: 这个 Run 已经结束了", ErrConflict)
	}
	return r, nil
}

// dmLiveRun is the task's work or chat run still going, if any.
func (e *Engine) dmLiveRun(ctx context.Context, taskID string) (store.Run, bool) {
	r := e.lastRun(ctx, taskID, event.PurposeWork, event.PurposeChat)
	return r, r.ID != "" && r.Status == event.RunStatusRunning
}

// dmSessionRun is the newest work or chat run that left an agent session.
func (e *Engine) dmSessionRun(ctx context.Context, taskID string) (store.Run, bool) {
	rs, err := e.store().Runs(ctx, store.RunFilter{TaskID: taskID})
	if err != nil {
		return store.Run{}, false
	}
	for i := len(rs) - 1; i >= 0; i-- {
		r := rs[i]
		if (r.Purpose == event.PurposeWork || r.Purpose == event.PurposeChat) && r.Session != "" {
			return r, true
		}
	}
	return store.Run{}, false
}

func (e *Engine) dmToRun(ctx context.Context, project store.Project, in DMInput) (string, error) {
	run, err := e.dmRunInProject(ctx, in.To.RunID, project)
	if err != nil {
		return "", err
	}
	t, err := e.store().Task(ctx, run.TaskID)
	if err != nil {
		return "", fmt.Errorf("%w: 这个 Run 不属于任务", ErrInvalid)
	}
	msgID, err := e.dmMessage(ctx, project, t, in, run.ID, run.Agent)
	if err != nil {
		return "", err
	}
	if err := e.Nodes.MessageAs(ctx, run.ID, in.Content, event.ActorUser); err != nil {
		return "", err
	}
	return msgID, nil
}

func (e *Engine) dmToTask(ctx context.Context, project store.Project, in DMInput) (string, error) {
	t, err := e.store().Task(ctx, in.To.TaskID)
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("%w: 这个任务不存在", ErrInvalid)
	}
	if err != nil {
		return "", err
	}
	if t.ProjectID != project.ID {
		return "", fmt.Errorf("%w: 这个任务不在这个项目里", ErrInvalid)
	}
	if t.Status == event.TaskQueued || t.Status == event.TaskWaitingApproval {
		return "", fmt.Errorf("%w: 任务正在排队或等审批，先处理完再私聊", ErrConflict)
	}
	if live, ok := e.dmLiveRun(ctx, t.ID); ok {
		// The task is in a run already: this is a follow-up to it.
		msgID, err := e.dmMessage(ctx, project, t, in, live.ID, live.Agent)
		if err != nil {
			return "", err
		}
		if err := e.Nodes.MessageAs(ctx, live.ID, in.Content, event.ActorUser); err != nil {
			return "", err
		}
		return msgID, nil
	}
	prev, ok := e.dmSessionRun(ctx, t.ID)
	if !ok {
		return "", fmt.Errorf("%w: 这个任务的 agent 没有留下会话，不能接着聊", ErrConflict)
	}

	runID := ids.New()
	msgID, err := e.dmMessage(ctx, project, t, in, runID, prev.Agent)
	if err != nil {
		return "", err
	}
	persona := e.personaOf(ctx, t, project)
	mode := firstNonEmpty(t.PermissionMode, persona.DefaultMode, project.DefaultMode, e.setting(ctx, SettingDefaultMode), proto.ModeAuto)
	repoPath := prev.RepoPath
	if t.Place == event.PlaceScratch {
		repoPath = ""
	}
	cr := nodehub.CreateRun{
		ThreadID: in.ThreadID, NodeID: prev.NodeID, Agent: prev.Agent, RepoPath: repoPath,
		TaskID: t.ID, Purpose: event.PurposeChat, PermissionMode: mode,
		Branch: t.Branch, BaseBranch: t.BaseBranch, BaseFrom: t.BaseFrom,
		Title: "私聊：" + t.Title, ResumeSession: prev.Session,
		Prompt: in.Content, ContinuePrompt: in.Content, RunID: runID, ChatFrom: t.Status,
		Persona: persona.Name, Options: mergeOptions(persona.Options, t.Options),
	}
	if t.Place == event.PlaceInPlace {
		cr.Kind = proto.KindInPlace
	}
	if _, err := e.Nodes.Create(ctx, cr); err != nil {
		return "", err
	}
	return msgID, e.transition(ctx, t, event.TaskRunning, "用户在私聊", "", event.ActorUser)
}

func (e *Engine) dmConsult(ctx context.Context, project store.Project, in DMInput) (string, error) {
	msgID, err := e.dmMessage(ctx, project, store.Task{}, in, "", "顾问")
	if err != nil {
		return "", err
	}
	go func() {
		_, _ = e.Consult(context.WithoutCancel(ctx), ConsultInput{ProjectID: project.ID, Question: in.Content, ThreadID: in.ThreadID, TopicID: in.TopicID})
	}()
	return msgID, nil
}

// dmMessage stores the user's words as a dm: it starts no topic and is not
// addressed to the supervisor.
func (e *Engine) dmMessage(ctx context.Context, project store.Project, t store.Task, in DMInput, runID, agent string) (string, error) {
	topic := in.TopicID
	if topic == "" && t.ID != "" {
		topic = e.store().TaskTopic(ctx, in.ThreadID, t.ID)
	}
	msgID := ids.New()
	ev := event.New(event.MessageUser, event.ActorUser, in.ThreadID, event.MessagePayload{
		MessageID: msgID, Kind: event.KindDM, Content: in.Content, TopicID: topic,
		Source: &event.MessageSource{RunID: runID, TaskID: t.ID, Agent: agent, ProjectID: project.ID},
	})
	if t.ID != "" {
		ev.TaskID = t.ID
	}
	if _, err := e.Log.Append(ctx, ev); err != nil {
		return "", err
	}
	return msgID, nil
}

// afterChat ends a direct chat: the agent's answer goes into the project
// chat, and the task either closes with a report (it changed something) or
// goes back to the status the chat interrupted.
func (e *Engine) afterChat(ctx context.Context, r store.Run) error {
	t, err := e.store().Task(ctx, r.TaskID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.Status != event.TaskRunning {
		return nil // cancelled or moved on while the chat was running
	}
	project, _ := e.store().Project(ctx, t.ProjectID)
	content := r.Summary
	if r.Status == event.RunStatusFailed {
		content = "没能接着聊：" + firstLineOf(r.Error)
	}
	if strings.TrimSpace(content) != "" {
		ev := event.New(event.MessageAssistant, event.ActorSystem, r.ThreadID, event.MessagePayload{
			MessageID: ids.New(), Kind: event.KindDM, Content: content,
			TopicID: e.store().TaskTopic(ctx, r.ThreadID, t.ID),
			Source:  &event.MessageSource{RunID: r.ID, TaskID: t.ID, Agent: r.Agent, ProjectID: project.ID},
		})
		ev.TaskID = t.ID
		if _, err := e.Log.Append(ctx, ev); err != nil {
			return err
		}
	}
	if r.Outcome == proto.OutcomeChanged {
		return e.finish(ctx, t, r, "私聊后有改动")
	}
	return e.transition(ctx, t, firstNonEmpty(r.ChatFrom, event.TaskDone), "", "", event.ActorSystem)
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

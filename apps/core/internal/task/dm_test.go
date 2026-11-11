package task

import (
	"context"
	"errors"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// dmEnv is a project chat with one finished task whose work run left a
// session, and a paired machine (not online: Create takes the node id).
func dmEnv(t *testing.T) (*Engine, *eventlog.Log, store.Task) {
	t.Helper()
	e, log, _ := topicEngine(t)
	ctx := context.Background()
	appendEvent := func(ev event.Event) {
		if _, err := log.Append(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(event.New(event.NodePaired, event.ActorSystem, "", event.NodePayload{NodeID: "n1", Name: "测试机"}))
	appendEvent(event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{
		ProjectID: "p1", Name: "demo", DefaultAgent: "fake",
		Nodes: []event.ProjectNode{{NodeID: "n1", Path: "/tmp/demo"}},
	}))
	appendEvent(event.New(event.ThreadCreated, event.ActorUser, "pc1", event.ThreadCreatedPayload{Title: "demo 的对话", Kind: "project", ProjectID: "p1"}))

	tk, err := e.Create(ctx, Input{
		ThreadID: "pc1", ProjectID: "p1", Title: "改按钮", Place: event.PlaceWorktree,
		BaseBranch: "main", BaseFrom: event.BaseLocal, Actor: event.ActorSupervisor,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := event.New(event.RunQueued, event.ActorSupervisor, "pc1", event.RunQueuedPayload{
		NodeID: "n1", Agent: "fake", Purpose: event.PurposeWork, Prompt: "做", Branch: "task/1",
	})
	run.RunID, run.TaskID = "w1", tk.ID
	appendEvent(run)
	started := event.New(event.RunStarted, event.ActorSystem, "pc1", event.RunStartedPayload{Workdir: "/tmp/demo", Branch: "task/1", BaseBranch: "main"})
	started.RunID, started.TaskID = "w1", tk.ID
	appendEvent(started)
	end := event.New(event.RunEnded, event.ActorSystem, "pc1", event.RunEndedPayload{Status: event.RunStatusSucceeded, Summary: "做完了", SessionID: "s1"})
	end.RunID, end.TaskID = "w1", tk.ID
	appendEvent(end)
	if err := e.transition(ctx, tk, event.TaskDone, "", "", event.ActorSystem); err != nil {
		t.Fatal(err)
	}
	tk, _ = log.Store.Task(ctx, tk.ID)
	return e, log, tk
}

func TestDMStartsAChatRun(t *testing.T) {
	e, log, tk := dmEnv(t)
	ctx := context.Background()

	// Only project chats take direct messages.
	if _, err := e.DM(ctx, DMInput{ThreadID: "main", Content: "在吗", To: DMTarget{TaskID: tk.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a direct message outside a project chat: %v", err)
	}

	msgID, err := e.DM(ctx, DMInput{ThreadID: "pc1", Content: "把标题改成蓝色", To: DMTarget{TaskID: tk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := log.Store.Messages(ctx, "pc1", 0, 50)
	var dm store.Message
	for _, m := range msgs {
		if m.ID == msgID {
			dm = m
		}
	}
	if dm.Kind != event.KindDM || dm.TopicID != "" || dm.Source == nil || dm.Source.Agent != "fake" {
		t.Fatalf("the user's direct message: %+v", dm)
	}
	tk, _ = log.Store.Task(ctx, tk.ID)
	if tk.Status != event.TaskRunning {
		t.Fatalf("task status: %s", tk.Status)
	}
	var chat store.Run
	rs, _ := log.Store.Runs(ctx, store.RunFilter{TaskID: tk.ID})
	for _, r := range rs {
		if r.Purpose == event.PurposeChat {
			chat = r
		}
	}
	if chat.ID == "" || chat.ResumeSession != "s1" || chat.ContinuePrompt != "把标题改成蓝色" ||
		chat.ChatFrom != event.TaskDone || chat.Prompt != "把标题改成蓝色" {
		t.Fatalf("chat run: %+v", chat)
	}
}

func TestDMDeliversToARunningRun(t *testing.T) {
	e, log, tk := dmEnv(t)
	ctx := context.Background()
	r := event.New(event.RunQueued, event.ActorSupervisor, "pc1", event.RunQueuedPayload{
		NodeID: "n1", Agent: "fake", Purpose: event.PurposeWork, Prompt: "再改一轮", Branch: "task/1",
	})
	r.RunID, r.TaskID = "w2", tk.ID
	if _, err := log.Append(ctx, r); err != nil {
		t.Fatal(err)
	}
	started := event.New(event.RunStarted, event.ActorSystem, "pc1", event.RunStartedPayload{Workdir: "/tmp/demo"})
	started.RunID, started.TaskID = "w2", tk.ID
	if _, err := log.Append(ctx, started); err != nil {
		t.Fatal(err)
	}
	if err := e.transition(ctx, tk, event.TaskRunning, "", "", event.ActorSystem); err != nil {
		t.Fatal(err)
	}

	if _, err := e.DM(ctx, DMInput{ThreadID: "pc1", Content: "改成深蓝", To: DMTarget{TaskID: tk.ID}}); err != nil {
		t.Fatal(err)
	}
	events, err := log.Store.RunEvents(ctx, "w2")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == event.RunMessage && ev.RunID == "w2" && ev.Actor == event.ActorUser {
			var p event.RunMessagePayload
			_ = ev.Decode(&p)
			found = p.Text == "改成深蓝"
		}
	}
	if !found {
		t.Fatal("the message did not reach the running run")
	}
	var chat int
	rs, _ := log.Store.Runs(ctx, store.RunFilter{TaskID: tk.ID})
	for _, x := range rs {
		if x.Purpose == event.PurposeChat {
			chat++
		}
	}
	if chat != 0 {
		t.Fatalf("a chat run was opened while one was going: %d", chat)
	}
}

func TestAfterChatReturnsToTheInterruptedStatus(t *testing.T) {
	e, log, tk := dmEnv(t)
	ctx := context.Background()
	if _, err := e.DM(ctx, DMInput{ThreadID: "pc1", Content: "还好吗", To: DMTarget{TaskID: tk.ID}}); err != nil {
		t.Fatal(err)
	}
	rs, _ := log.Store.Runs(ctx, store.RunFilter{TaskID: tk.ID})
	var chat store.Run
	for _, r := range rs {
		if r.Purpose == event.PurposeChat {
			chat = r
		}
	}
	// No changes: back to the status it interrupted, and no report.
	chat.Status = event.RunStatusSucceeded
	if err := e.afterChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	tk, _ = log.Store.Task(ctx, tk.ID)
	if tk.Status != event.TaskDone {
		t.Fatalf("task status after a chat without changes: %s", tk.Status)
	}
	msgs, _ := log.Store.Messages(ctx, "pc1", 0, 50)
	if len(msgs) != 1 || msgs[0].Kind != event.KindDM {
		t.Fatalf("a chat without changes reported something: %+v", msgs)
	}

	// A chat that changed something closes the task and reports it.
	if err := e.transition(ctx, tk, event.TaskRunning, "用户在私聊", "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DM(ctx, DMInput{ThreadID: "pc1", Content: "把标题改了", To: DMTarget{TaskID: tk.ID}}); err != nil {
		t.Fatal(err)
	}
	rs, _ = log.Store.Runs(ctx, store.RunFilter{TaskID: tk.ID})
	for _, r := range rs {
		if r.Purpose == event.PurposeChat && r.Status == event.RunStatusQueued {
			chat = r
		}
	}
	chat.Status, chat.Outcome, chat.DiffStat, chat.Summary = event.RunStatusSucceeded, "changed", " 1 file changed", "改了 README"
	if err := e.afterChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	tk, _ = log.Store.Task(ctx, tk.ID)
	if tk.Status != event.TaskDone || tk.GitState != event.GitUnmerged {
		t.Fatalf("task after a chat with changes: %+v", tk)
	}
	msgs, _ = log.Store.Messages(ctx, "pc1", 0, 50)
	reports := 0
	for _, m := range msgs {
		if m.Kind == event.KindReport {
			reports++
		}
	}
	if reports != 1 {
		t.Fatalf("reports after a chat with changes: %d (%+v)", reports, msgs)
	}
}

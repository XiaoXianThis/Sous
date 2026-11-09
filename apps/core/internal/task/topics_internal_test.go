package task

import (
	"context"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func topicEngine(t *testing.T) (*Engine, *eventlog.Log, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.New(st, event.NewBus())
	nodes, err := nodehub.New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	main, err := log.EnsureMainThread(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return New(log, nodes), log, main
}

// Tasks belong to the topic of the turn that made them; subtasks to their
// parent's; reports and wake-ups go into the task's topic.
func TestTasksFollowTheirTopic(t *testing.T) {
	e, log, main := topicEngine(t)
	ctx := context.Background()
	log.Append(ctx, event.New(event.MessageUser, event.ActorUser, main, event.MessagePayload{MessageID: "t1", Content: "做个东西"}))
	turn := tool.WithTurn(ctx, tool.Turn{ThreadID: main, MainThreadID: main, TopicID: "t1"})
	parent, err := e.Create(turn, Input{ThreadID: main, Title: "父任务", Place: event.PlaceScratch, Actor: event.ActorSupervisor})
	if err != nil || parent.TopicID != "t1" {
		t.Fatalf("parent: %+v %v", parent, err)
	}
	child, err := e.Create(ctx, Input{ThreadID: main, ParentID: parent.ID, Title: "子任务", Place: event.PlaceScratch, Actor: event.ActorSupervisor})
	if err != nil || child.TopicID != "t1" {
		t.Fatalf("child: %+v %v", child, err)
	}
	e.post(ctx, parent, "父任务做完了", false)
	e.wake(ctx, parent, "k1", "任务有新进展", "看看")
	msgs, _ := log.Store.TopicMessages(ctx, main, "t1", 0, 10)
	// The question, the report, the report's own wake-up and the second one.
	if len(msgs) != 4 || msgs[1].Kind != event.KindReport || msgs[1].TaskID != parent.ID || msgs[3].Kind != event.KindTrigger || msgs[3].TaskID != parent.ID {
		t.Fatalf("topic t1: %+v", msgs)
	}
}

// A task made without a topic gets the one its first report starts.
func TestFirstReportStartsATopic(t *testing.T) {
	e, log, main := topicEngine(t)
	ctx := context.Background()
	tk, err := e.Create(ctx, Input{ThreadID: main, Title: "页面上建的任务", Place: event.PlaceScratch})
	if err != nil || tk.TopicID != "" {
		t.Fatalf("task: %+v %v", tk, err)
	}
	e.post(ctx, tk, "第一次汇报", false)
	tk, _ = log.Store.Task(ctx, tk.ID)
	if tk.TopicID == "" {
		t.Fatal("the report did not give the task a topic")
	}
	topic, err := log.Store.Topic(ctx, tk.TopicID)
	if err != nil || topic.Title != "页面上建的任务" {
		t.Fatalf("topic: %+v %v", topic, err)
	}
	e.post(ctx, tk, "第二次汇报", false)
	msgs, _ := log.Store.TopicMessages(ctx, main, tk.TopicID, 0, 10)
	found := false
	for _, m := range msgs {
		found = found || m.Content == "第二次汇报"
	}
	if !found {
		t.Fatalf("second report: %+v", msgs)
	}
}

// The talk between the supervisor and a task's agents, in order.
func TestTaskTalk(t *testing.T) {
	e, log, main := topicEngine(t)
	ctx := context.Background()
	tk, _ := e.Create(ctx, Input{ThreadID: main, Title: "对话", Place: event.PlaceScratch})
	stamp := func(ev event.Event, run string) event.Event {
		ev.TaskID, ev.RunID = tk.ID, run
		return ev
	}
	log.Append(ctx,
		stamp(event.New(event.RunQueued, event.ActorSupervisor, main, event.RunQueuedPayload{NodeID: "n", Agent: "claude", Purpose: event.PurposeWork, Prompt: "把按钮改成蓝色"}), "r1"),
		stamp(event.New(event.RunUpdate, event.ActorSystem, main, event.RunUpdatePayload{Kind: "tool", Title: "edit"}), "r1"),
		stamp(event.New(event.RunUpdate, event.ActorSystem, main, event.RunUpdatePayload{Kind: "status", Text: "进度：改完一半"}), "r1"),
		stamp(event.New(event.TriggerFired, event.ActorTrigger, main, event.TriggerPayload{Kind: event.TriggerTask, Key: "ask:r1:1", MessageID: "q1", Content: "包装", Quote: "要深蓝还是浅蓝？"}), "r1"),
		stamp(event.New(event.RunMessage, event.ActorSupervisor, main, event.RunMessagePayload{MessageID: "m1", Text: "深蓝"}), "r1"),
		stamp(event.New(event.RunEnded, event.ActorSystem, main, event.RunEndedPayload{Status: event.RunStatusSucceeded, Summary: "改好了"}), "r1"),
		stamp(event.New(event.RunQueued, event.ActorSupervisor, main, event.RunQueuedPayload{NodeID: "n", Agent: "sous", Purpose: event.PurposeGit}), "g1"),
		stamp(event.New(event.RunEnded, event.ActorSystem, main, event.RunEndedPayload{Status: event.RunStatusSucceeded, Summary: "合并了"}), "g1"),
	)
	talk, err := e.TaskTalk(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ kind, from, text string }{
		{"prompt", "supervisor", "把按钮改成蓝色"},
		{"progress", "agent", "改完一半"},
		{"ask", "agent", "要深蓝还是浅蓝？"},
		{"message", "supervisor", "深蓝"},
		{"result", "agent", "改好了"},
	}
	if len(talk) != len(want) {
		t.Fatalf("talk: %+v", talk)
	}
	for i, w := range want {
		if talk[i].Kind != w.kind || talk[i].From != w.from || talk[i].Text != w.text || talk[i].Agent != "claude" {
			t.Fatalf("talk[%d] = %+v, want %+v", i, talk[i], w)
		}
	}
}

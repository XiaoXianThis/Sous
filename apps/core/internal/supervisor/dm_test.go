package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// A direct message to an agent is not a question for the supervisor, and
// its answer is not a reply to the user's topic (decision 0028).
func TestDMsAreNotForTheSupervisor(t *testing.T) {
	msgs := []store.Message{
		{Seq: 1, Role: "user", Content: "主线问题", TopicID: "1"},
		{Seq: 2, Role: "user", Kind: event.KindDM, Content: "私聊", TopicID: "1"},
		{Seq: 3, Role: "assistant", Kind: event.KindStep, ReplyTo: 1},
		{Seq: 4, Role: "assistant", Kind: event.KindDM, Content: "私聊的回答", ReplyTo: 2},
	}
	if p := pendingTopics(msgs); len(p) != 1 || p[0].ID != "1" || p[0].Newest != 1 {
		t.Fatalf("pending topics: %+v", p)
	}
	if got := unanswered(msgs); got != 1 {
		t.Fatalf("unanswered = %d, want 1", got)
	}
	if got := oldestPending(msgs); got != 1 {
		t.Fatalf("oldest pending = %d, want 1", got)
	}
	for _, m := range forTopic(msgs, "1") {
		if m.Kind == event.KindDM {
			t.Fatalf("a direct message reached the history: %+v", m)
		}
	}
}

// The tail lists direct messages by agent and task, newest first.
func TestDMLinesSummarizeByAgentAndTask(t *testing.T) {
	p := &answering{fn: reply("好的")}
	_, _, sup := setup(t, p, nil)
	msgs := []store.Message{
		{Seq: 5, Role: "user", Kind: event.KindDM, Content: "a", TaskID: "t1", CreatedAt: 100, Source: &event.MessageSource{Agent: "claude"}},
		{Seq: 6, Role: "user", Kind: event.KindDM, Content: "b", TaskID: "t1", CreatedAt: 200, Source: &event.MessageSource{Agent: "claude"}},
		{Seq: 7, Role: "user", Kind: event.KindDM, Content: "c", CreatedAt: 300, Source: &event.MessageSource{Agent: "顾问"}},
	}
	lines := sup.dmLines(context.Background(), 4, msgs)
	if len(lines) != 2 {
		t.Fatalf("lines: %+v", lines)
	}
	if !strings.HasPrefix(lines[0], "项目顾问：1 条") || !strings.Contains(lines[0], "(#7)") && !strings.Contains(lines[0], "（#7）") {
		t.Fatalf("first line: %q", lines[0])
	}
	if !strings.Contains(lines[1], "claude · 任务 t1") || !strings.Contains(lines[1], "：2 条") || !strings.Contains(lines[1], "#6") {
		t.Fatalf("second line: %q", lines[1])
	}
	if extra := sup.dmLines(context.Background(), 6, msgs); len(extra) != 1 {
		t.Fatalf("messages before the segment were counted: %+v", extra)
	}
	tail := prompts.Tail(prompts.TurnEnv{DMs: lines})
	if !strings.Contains(tail, "## 用户的私聊") || !strings.Contains(tail, "claude · 任务 t1") {
		t.Fatalf("tail: %s", tail)
	}
}

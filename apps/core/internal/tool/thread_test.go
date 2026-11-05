package tool_test

import (
	"context"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func post(t *testing.T, log *eventlog.Log, thread, role, text string) int64 {
	t.Helper()
	typ, actor := event.MessageUser, event.ActorUser
	if role == "assistant" {
		typ, actor = event.MessageAssistant, event.ActorSupervisor
	}
	evs, err := log.Append(context.Background(), event.New(typ, actor, thread, event.MessagePayload{MessageID: text, Content: text}))
	if err != nil {
		t.Fatal(err)
	}
	return evs[0].Seq
}

func TestThreadSearchAndRead(t *testing.T) {
	log, _, ctx, main := setup(t)
	reg := tool.NewRegistry(tool.ThreadSearch{Log: log}, tool.ThreadRead{Log: log})
	post(t, log, main, "user", "Hub 打算部署到哪台服务器？")
	seq := post(t, log, main, "assistant", "定了：部署到 agent.wxian.cc，用 Caddy 反代。"+strings.Repeat("细节", 4000))
	post(t, log, main, "user", "好的")

	r := call(reg, ctx, "thread_search", map[string]any{"query": "Caddy 反代"})
	if r.IsError || !strings.Contains(r.Content, "#") || !strings.Contains(r.Content, "主管") || len([]rune(r.Content)) > 400 {
		t.Fatalf("search: %+v", r)
	}
	r = call(reg, ctx, "thread_read", map[string]any{"seq": seq, "before": 1, "after": 1})
	if r.IsError || !strings.Contains(r.Content, "Hub 打算部署") || !strings.Contains(r.Content, "好的") || !strings.Contains(r.Content, "后面还有") {
		t.Fatalf("read: %+v", r)
	}
	if r = call(reg, ctx, "thread_read", map[string]any{"seq": 99999}); !r.IsError {
		t.Fatalf("missing seq: %+v", r)
	}

	// scope=all is only for the main thread.
	evs, _ := log.Append(context.Background(), event.New(event.ThreadCreated, event.ActorUser, "t2", event.ThreadCreatedPayload{Title: "x", Kind: "chat"}))
	other := evs[0].ThreadID
	post(t, log, other, "user", "另一条对话里也提到 Caddy")
	r = call(reg, ctx, "thread_search", map[string]any{"query": "Caddy", "scope": "all"})
	if r.IsError || !strings.Contains(r.Content, "另一条对话") {
		t.Fatalf("search all: %+v", r)
	}
	inOther := tool.WithTurn(context.Background(), tool.Turn{ThreadID: other, MainThreadID: main})
	if r = call(reg, inOther, "thread_search", map[string]any{"query": "Caddy", "scope": "all"}); !r.IsError {
		t.Fatalf("scope=all outside the main thread: %+v", r)
	}
}

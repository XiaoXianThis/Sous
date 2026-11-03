package tool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func setup(t *testing.T) (*eventlog.Log, *tool.Registry, context.Context, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := eventlog.New(st, event.NewBus())
	main, err := log.EnsureMainThread(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry(tool.MemorySave{Log: log}, tool.MemorySearch{Log: log}, tool.MemoryForget{Log: log}, tool.Notify{Log: log})
	ctx := tool.WithTurn(context.Background(), tool.Turn{ThreadID: main, MainThreadID: main})
	return log, reg, ctx, main
}

func call(reg *tool.Registry, ctx context.Context, name string, args any) provider.ToolResult {
	raw, _ := json.Marshal(args)
	return reg.Call(ctx, provider.ToolCall{ID: "c", Name: name, Args: raw})
}

func TestMemoryToolsRoundTrip(t *testing.T) {
	log, reg, ctx, _ := setup(t)
	r := call(reg, ctx, "memory_save", map[string]string{"content": "用户每天早上九点开始工作", "kind": "profile"})
	if r.IsError || !strings.HasPrefix(r.Content, "已记住 [") {
		t.Fatalf("save: %+v", r)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.Content, "已记住 ["), "]")

	r = call(reg, ctx, "memory_search", map[string]string{"query": "几点开始工作"})
	if r.IsError || !strings.Contains(r.Content, id) || !strings.Contains(r.Content, "(profile)") {
		t.Fatalf("search: %+v", r)
	}
	r = call(reg, ctx, "memory_save", map[string]string{"id": id, "content": "用户每天早上十点开始工作", "kind": "profile"})
	if r.IsError || !strings.HasPrefix(r.Content, "已更新") {
		t.Fatalf("update: %+v", r)
	}
	if m, _ := log.Store.Memory(ctx, id); m.Content != "用户每天早上十点开始工作" {
		t.Fatalf("after update: %+v", m)
	}
	if r = call(reg, ctx, "memory_forget", map[string]string{"id": id}); r.IsError {
		t.Fatalf("forget: %+v", r)
	}
	if r = call(reg, ctx, "memory_search", map[string]string{"query": "开始工作"}); r.Content != "没有找到相关记忆。" {
		t.Fatalf("after forget: %+v", r)
	}
}

func TestMemoryToolsValidate(t *testing.T) {
	_, reg, ctx, _ := setup(t)
	cases := []map[string]string{
		{"content": "  ", "kind": "note"},
		{"content": "x", "kind": "secret"},
		{"content": "x", "kind": "note", "id": "missing"},
		{"content": strings.Repeat("长", 2001), "kind": "note"},
	}
	for _, c := range cases {
		if r := call(reg, ctx, "memory_save", c); !r.IsError {
			t.Errorf("expected error for %v", c)
		}
	}
	if r := call(reg, ctx, "memory_forget", map[string]string{"id": "missing"}); !r.IsError {
		t.Error("forget of missing memory succeeded")
	}
}

func TestNotifyPostsNoticeToMainThread(t *testing.T) {
	log, reg, _, main := setup(t)
	other := tool.WithTurn(context.Background(), tool.Turn{ThreadID: "other"}) // main is looked up
	if r := call(reg, other, "notify", map[string]string{"text": "构建失败了，需要你看一下"}); r.IsError {
		t.Fatalf("notify: %+v", r)
	}
	msgs, _ := log.Store.Messages(context.Background(), main, 0, 10)
	if len(msgs) != 1 || msgs[0].Kind != event.KindNotice || msgs[0].Content != "构建失败了，需要你看一下" {
		t.Fatalf("main thread: %+v", msgs)
	}
	if r := call(reg, other, "notify", map[string]string{"text": ""}); !r.IsError {
		t.Fatal("empty notify accepted")
	}
}

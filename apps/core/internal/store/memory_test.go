package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func putMemory(t *testing.T, s *store.Store, id, kind, content string) {
	t.Helper()
	if _, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Append(event.New(event.MemoryPut, event.ActorSupervisor, "", event.MemoryPayload{MemoryID: id, Kind: kind, Content: content}))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func ids(ms []store.Memory) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestMemorySearchChinese(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	putMemory(t, s, "m1", "profile", "用户喜欢简洁直接的回复，不要客套")
	putMemory(t, s, "m2", "project", "sous 项目用 Go 和 Svelte 编写，仓库遵循 wokspace 规范")
	putMemory(t, s, "m3", "lesson", "DeepSeek 的 deepseek-chat 已下线，要用 deepseek-v4-pro")

	got, err := s.SearchMemories(ctx, "回复要怎么写？我喜欢简洁的", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "m1" {
		t.Fatalf("chinese search: %v", ids(got))
	}
	if got, _ := s.SearchMemories(ctx, "Go", 5); len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("short word: %v", ids(got))
	}
	if got, _ := s.SearchMemories(ctx, "deepseek 模型", 5); len(got) == 0 || got[0].ID != "m3" {
		t.Fatalf("mixed: %v", ids(got))
	}
	if got, _ := s.SearchMemories(ctx, "   ？！", 5); len(got) != 0 {
		t.Fatalf("empty query: %v", ids(got))
	}
}

func TestMemoryUpdateAndDelete(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	putMemory(t, s, "m1", "note", "旧内容：周五开会")
	putMemory(t, s, "m1", "note", "新内容：周四开会")
	m, err := s.Memory(ctx, "m1")
	if err != nil || m.Content != "新内容：周四开会" {
		t.Fatalf("update: %v %+v", err, m)
	}
	if got, _ := s.SearchMemories(ctx, "周五", 5); len(got) != 0 {
		t.Fatalf("old text still indexed: %v", ids(got))
	}
	if got, _ := s.SearchMemories(ctx, "周四开会", 5); len(got) != 1 {
		t.Fatalf("new text not indexed: %v", ids(got))
	}
	if _, _, err := s.Write(ctx, func(w *store.Writer) error {
		_, err := w.Append(event.New(event.MemoryDeleted, event.ActorUser, "", event.MemoryPayload{MemoryID: "m1"}))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Memory(ctx, "m1"); err != store.ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
	if got, _ := s.SearchMemories(ctx, "周四开会", 5); len(got) != 0 {
		t.Fatalf("deleted memory still indexed: %v", ids(got))
	}
}

func TestSearchTerms(t *testing.T) {
	got := store.SearchTermsForTest("后端用 Go 写？DeepSeek-v4 好")
	want := []string{"后端", "端用", "go", "deepseek", "v4"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("terms = %q, want %q", got, want)
	}
}

func TestMemoriesByKind(t *testing.T) {
	s := open(t)
	putMemory(t, s, "a", "profile", "喜欢中文")
	putMemory(t, s, "b", "note", "随手记")
	putMemory(t, s, "c", "profile", "早上不开会")
	got, err := s.Memories(context.Background(), "profile", 10)
	if err != nil || len(got) != 2 || got[0].ID != "c" {
		t.Fatalf("by kind: %v %v", err, ids(got))
	}
	if all, _ := s.Memories(context.Background(), "", 10); len(all) != 3 {
		t.Fatalf("all: %v", ids(all))
	}
}

func TestMessageKind(t *testing.T) {
	s := open(t)
	th := newThread(t, s, "main")
	s.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Append(event.New(event.MessageAssistant, "supervisor", th, event.MessagePayload{MessageID: "n1", Content: "提醒", Kind: event.KindNotice}))
		return err
	})
	msgs, _ := s.Messages(context.Background(), th, 0, 10)
	if len(msgs) != 1 || msgs[0].Kind != "notice" {
		t.Fatalf("kind: %+v", msgs)
	}
}

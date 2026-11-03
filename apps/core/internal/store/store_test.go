package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newThread(t *testing.T, s *store.Store, kind string) string {
	t.Helper()
	ev := event.New(event.ThreadCreated, event.ActorUser, "", event.ThreadCreatedPayload{Title: "t", Kind: kind})
	ev.ThreadID = ev.ID
	if _, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Append(ev)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ev.ThreadID
}

func appendMsg(t *testing.T, s *store.Store, typ event.Type, thread, id, content string) event.Event {
	t.Helper()
	evs, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Append(event.New(typ, event.ActorUser, thread, event.MessagePayload{MessageID: id, Content: content}))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return evs[0]
}

func TestAppendAssignsIncreasingSeqAndProjects(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	th := newThread(t, s, "chat")
	a := appendMsg(t, s, event.MessageUser, th, "m1", "你好")
	b := appendMsg(t, s, event.MessageAssistant, th, "m2", "在的")
	if a.Seq <= 0 || b.Seq <= a.Seq {
		t.Fatalf("seq not increasing: %d, %d", a.Seq, b.Seq)
	}
	msgs, err := s.Messages(ctx, th, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[1].Content != "在的" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	evs, err := s.EventsSince(ctx, a.Seq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].ID != b.ID {
		t.Fatalf("EventsSince: %+v", evs)
	}
}

func TestMessagesPaginationOldestFirst(t *testing.T) {
	s := open(t)
	th := newThread(t, s, "chat")
	var seqs []int64
	for i := range 5 {
		ev := appendMsg(t, s, event.MessageUser, th, string(rune('a'+i)), "x")
		seqs = append(seqs, ev.Seq)
	}
	got, err := s.Messages(context.Background(), th, seqs[4], 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != seqs[2] || got[1].Seq != seqs[3] {
		t.Fatalf("page: %+v", got)
	}
}

func TestFailedWriteRollsBack(t *testing.T) {
	s := open(t)
	th := newThread(t, s, "chat")
	_, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		if _, err := w.Append(event.New(event.MessageUser, "user", th, event.MessagePayload{MessageID: "dup", Content: "1"})); err != nil {
			return err
		}
		_, err := w.Append(event.New(event.MessageUser, "user", th, event.MessagePayload{MessageID: "dup", Content: "2"}))
		return err
	})
	if err == nil {
		t.Fatal("expected duplicate message id to fail")
	}
	msgs, _ := s.Messages(context.Background(), th, 0, 10)
	if len(msgs) != 0 {
		t.Fatalf("write was not rolled back: %+v", msgs)
	}
}

func TestEphemeralEventsRejected(t *testing.T) {
	s := open(t)
	_, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Append(event.New(event.MessageAssistantDelta, "supervisor", "x", event.DeltaPayload{}))
		return err
	})
	if err == nil {
		t.Fatal("ephemeral event was stored")
	}
}

func TestOnlyOneMainThread(t *testing.T) {
	s := open(t)
	newThread(t, s, "main")
	ev := event.New(event.ThreadCreated, "system", "", event.ThreadCreatedPayload{Title: "2", Kind: "main"})
	ev.ThreadID = ev.ID
	_, _, err := s.Write(context.Background(), func(w *store.Writer) error { _, err := w.Append(ev); return err })
	if err == nil {
		t.Fatal("second main thread allowed")
	}
}

func TestEffectsQueueDedupAndRetry(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for range 2 {
		if _, _, err := s.Write(ctx, func(w *store.Writer) error { return w.Enqueue("push", "k1", []byte("p")) }); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.DueEffects(ctx, time.Now(), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("due: %v %+v", err, due)
	}
	later := time.Now().Add(time.Hour)
	if err := s.RetryEffect(ctx, due[0].ID, "boom", later); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.DueEffects(ctx, time.Now(), 10); len(due) != 0 {
		t.Fatalf("retried effect still due: %+v", due)
	}
	due, _ = s.DueEffects(ctx, later, 10)
	if len(due) != 1 || due[0].Attempts != 1 || due[0].LastError != "boom" {
		t.Fatalf("after retry: %+v", due)
	}
	if err := s.CompleteEffect(ctx, due[0].ID); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.DueEffects(ctx, later, 10); len(due) != 0 {
		t.Fatal("completed effect remains")
	}
}

func TestFileDatabaseReopenAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hub.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	th := newThread(t, s, "chat")
	appendMsg(t, s, event.MessageUser, th, "m1", "持久化")
	backup := filepath.Join(dir, "backup.db")
	if err := s.Backup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	s.Close()

	for _, p := range []string{path, backup} {
		s2, err := store.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		msgs, err := s2.Messages(context.Background(), th, 0, 10)
		s2.Close()
		if err != nil || len(msgs) != 1 || msgs[0].Content != "持久化" {
			t.Fatalf("%s: %v %+v", p, err, msgs)
		}
	}
}

func TestUsageProjection(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_, _, err := s.Write(ctx, func(w *store.Writer) error {
		for range 2 {
			if _, err := w.Append(event.New(event.UsageRecorded, "supervisor", "", event.UsagePayload{
				Provider: "deepseek", Model: "m", Role: "supervisor", InputTokens: 10, OutputTokens: 5, CachedTokens: 2,
			})); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.UsageSince(ctx, time.Now().Add(-time.Minute))
	if err != nil || len(rows) != 1 || rows[0].Calls != 2 || rows[0].InputTokens != 20 || rows[0].CachedTokens != 4 {
		t.Fatalf("usage: %v %+v", err, rows)
	}
}

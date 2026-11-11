package store_test

import (
	"context"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestNotesLifecycle(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	appendEvent := func(typ event.Type, p any) {
		t.Helper()
		if _, _, err := s.Write(ctx, func(w *store.Writer) error { _, err := w.Append(event.New(typ, event.ActorUser, "", p)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	text := "旧文字"
	appendEvent(event.NotePut, event.NotePayload{NoteID: "n", Text: &text})
	n, err := s.Note(ctx, "n")
	if err != nil || n.Text != text || n.Status != event.NoteOpen {
		t.Fatalf("create: %+v %v", n, err)
	}
	text = "中文内容 100%_"
	appendEvent(event.NotePut, event.NotePayload{NoteID: "n", Text: &text})
	appendEvent(event.NotePut, event.NotePayload{NoteID: "n", Status: event.NoteArchived})
	n, err = s.Note(ctx, "n")
	if err != nil || n.Text != text || n.Status != event.NoteArchived {
		t.Fatalf("update: %+v %v", n, err)
	}
	for _, q := range []string{"内容", "%_"} {
		got, err := s.Notes(ctx, store.NoteFilter{Query: q})
		if err != nil || len(got) != 1 {
			t.Fatalf("search %q: %+v %v", q, got, err)
		}
	}
	got, err := s.Notes(ctx, store.NoteFilter{Query: "%x"})
	if err != nil || len(got) != 0 {
		t.Fatalf("literal search: %+v %v", got, err)
	}
	appendEvent(event.NotePut, event.NotePayload{NoteID: "n", Status: event.NoteOpen})
	for i := 0; i < 2; i++ {
		appendEvent(event.NoteSent, event.NoteSentPayload{NoteIDs: []string{"n"}, ThreadID: "thread", TopicID: "topic", MessageID: "message"})
	}
	n, err = s.Note(ctx, "n")
	if err != nil || n.Status != event.NoteSentDone || len(n.Sends) != 2 {
		t.Fatalf("send: %+v %v", n, err)
	}
	appendEvent(event.NoteDeleted, event.NotePayload{NoteID: "n"})
	if _, err = s.Note(ctx, "n"); err != store.ErrNotFound {
		t.Fatalf("delete: %v", err)
	}
}

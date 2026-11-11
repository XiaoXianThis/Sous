package store_test

import (
	"context"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestMeetingProjection(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	appendEvent := func(ev event.Event) {
		t.Helper()
		if _, _, err := s.Write(ctx, func(w *store.Writer) error { _, err := w.Append(ev); return err }); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(event.New(event.ThreadCreated, event.ActorUser, "parent", event.ThreadCreatedPayload{Title: "parent", Kind: "main"}))
	appendEvent(event.New(event.ThreadCreated, event.ActorUser, "meeting", event.ThreadCreatedPayload{Title: "meeting", Kind: event.KindMeeting, ParentThreadID: "parent", MeetingTopic: "opening"}))
	appendEvent(event.New(event.MessageUser, event.ActorUser, "meeting", event.MessagePayload{MessageID: "opening", Content: "start"}))
	appendEvent(event.New(event.MessageUser, event.ActorUser, "meeting", event.MessagePayload{MessageID: "next", Content: "next"}))
	threads, err := s.Threads(ctx)
	if err != nil || len(threads) != 1 {
		t.Fatalf("threads: %+v %v", threads, err)
	}
	meetings, err := s.Meetings(ctx, "parent")
	if err != nil || len(meetings) != 1 || meetings[0].MeetingStatus != "open" {
		t.Fatalf("meetings: %+v %v", meetings, err)
	}
	other, err := s.Meetings(ctx, "other")
	if err != nil || len(other) != 0 {
		t.Fatalf("other: %+v %v", other, err)
	}
	msgs, err := s.Messages(ctx, "meeting", 0, 10)
	if err != nil || len(msgs) != 2 || msgs[0].TopicID != "opening" || msgs[1].TopicID != "opening" {
		t.Fatalf("messages: %+v %v", msgs, err)
	}
	linked, err := s.LinkedMeeting(ctx, "parent", "meeting")
	if err != nil || linked {
		t.Fatalf("before: %v %v", linked, err)
	}
	appendEvent(event.New(event.MessageAssistant, event.ActorSupervisor, "parent", event.MessagePayload{MessageID: "minutes", Kind: event.KindMinutes, Source: &event.MessageSource{MeetingID: "meeting"}}))
	linked, err = s.LinkedMeeting(ctx, "parent", "meeting")
	if err != nil || !linked {
		t.Fatalf("after: %v %v", linked, err)
	}
}

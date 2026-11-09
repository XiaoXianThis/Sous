package store_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func putEv(t *testing.T, s *store.Store, evs ...event.Event) {
	t.Helper()
	if _, _, err := s.Write(context.Background(), func(w *store.Writer) error {
		for _, ev := range evs {
			if _, err := w.Append(ev); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// clock gives the test's events strictly increasing timestamps: events made
// within one millisecond would otherwise tie, and topic order is by time.
var clock atomic.Int64

func stamp(ev event.Event) event.Event {
	clock.CompareAndSwap(0, time.Now().UnixMilli())
	ev.TS = clock.Add(1)
	return ev
}

func msg(typ event.Type, thread string, p event.MessagePayload) event.Event {
	return stamp(event.New(typ, event.ActorUser, thread, p))
}

func TestTopicProjection(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	th := newThread(t, s, "main")
	long := strings.Repeat("长", 40)
	put(t, s,
		msg(event.MessageUser, th, event.MessagePayload{MessageID: "a", Content: "\n  第一行   标题\n第二行"}),
		msg(event.MessageUser, th, event.MessagePayload{MessageID: "b", Content: long}),
		msg(event.MessageAssistant, th, event.MessagePayload{MessageID: "r", Content: "好", TopicID: "a", ReplyTo: 1}),
		msg(event.MessageAssistant, th, event.MessagePayload{MessageID: "plain", Content: "没有话题的回复"}),
		msg(event.MessageAssistant, th, event.MessagePayload{MessageID: "rep", Content: "汇报", Kind: event.KindReport, TopicTitle: "任务 X"}),
	)
	a, err := s.Topic(ctx, "a")
	if err != nil || a.Title != "第一行 标题" || a.Status != event.TopicOpen {
		t.Fatalf("a: %+v %v", a, err)
	}
	if b, _ := s.Topic(ctx, "b"); b.Title != strings.Repeat("长", 30)+"…" {
		t.Fatalf("b: %q", b.Title)
	}
	if rep, err := s.Topic(ctx, "rep"); err != nil || rep.Title != "任务 X" {
		t.Fatalf("a report starts its topic: %+v %v", rep, err)
	}
	if _, err := s.Topic(ctx, "plain"); err != store.ErrNotFound {
		t.Fatal("a plain reply must not start a topic")
	}

	closed, sum := event.TopicClosed, "做完了"
	put(t, s, stamp(event.New(event.TopicUpdated, event.ActorSupervisor, th, event.TopicUpdatedPayload{TopicID: "a", Status: &closed, Summary: &sum})))
	// The supervisor's own reply after closing keeps it closed…
	put(t, s, msg(event.MessageAssistant, th, event.MessagePayload{MessageID: "r2", Content: "已关闭", TopicID: "a"}))
	if a, _ = s.Topic(ctx, "a"); a.Status != event.TopicClosed || a.Summary != "做完了" {
		t.Fatalf("closed: %+v", a)
	}
	// …a report brings it back.
	put(t, s, msg(event.MessageAssistant, th, event.MessagePayload{MessageID: "r3", Content: "新汇报", Kind: event.KindReport, TopicID: "a"}))
	if a, _ = s.Topic(ctx, "a"); a.Status != event.TopicOpen {
		t.Fatalf("reopened: %+v", a)
	}

	put(t, s, stamp(event.New(event.MessageMoved, event.ActorSupervisor, th, event.MessageMovedPayload{MessageID: "b", TopicID: "a", From: "b"})))
	msgs, _ := s.TopicMessages(ctx, th, "a", 0, 50)
	ids := []string{}
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "a,b,r,r2,r3" {
		t.Fatalf("topic a: %v", ids)
	}
	stats, _ := s.TopicStats(ctx, th)
	if stats["a"].Count != 5 || stats["a"].Last == nil || stats["a"].Last.ID != "r3" || stats[""].Count != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if topics, _ := s.Topics(ctx, th, 10); len(topics) != 3 || topics[0].ID != "a" {
		t.Fatalf("topics: %+v", topics)
	}
}

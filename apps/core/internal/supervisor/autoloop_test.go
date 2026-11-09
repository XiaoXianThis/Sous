package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func setupBatch(t *testing.T, p provider.Provider, batch time.Duration) (*eventlog.Log, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.New(st, event.NewBus())
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(Options{Log: log, Provider: p, Model: "m", TaskBatch: batch})
	sup.Start(ctx)
	t.Cleanup(func() { cancel(); sup.Wait(); st.Close() })
	main, err := log.EnsureMainThread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return log, main
}

// taskNews fires task news; all of it is about tasks of one topic, the
// one the first news started.
func taskNews(t *testing.T, log *eventlog.Log, thread, key, content string) {
	t.Helper()
	topic := ""
	if ts, _ := log.Store.Topics(context.Background(), thread, 100); len(ts) > 0 {
		topic = ts[len(ts)-1].ID
	}
	ev := event.New(event.TriggerFired, event.ActorTrigger, thread, event.TriggerPayload{
		Kind: event.TriggerTask, Key: key, MessageID: key, Title: "任务有新进展", Content: content, TopicID: topic,
	})
	if _, err := log.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func replies(n int, text string) []provider.Response {
	out := make([]provider.Response, n)
	for i := range out {
		out[i] = provider.Response{Message: provider.Text(provider.RoleAssistant, fmt.Sprintf("%s %d", text, i))}
	}
	return out
}

// Task news that arrives together is taken up in one turn (decision 0025).
func TestTaskNewsIsBatched(t *testing.T) {
	p := &scripted{responses: replies(3, "好")}
	log, main := setupBatch(t, p, 150*time.Millisecond)
	taskNews(t, log, main, "a", "任务 A 完成了")
	taskNews(t, log, main, "b", "任务 B 失败了")
	taskNews(t, log, main, "c", "任务 C 等你验收")
	msgs := waitMessages(t, log, main, 4)
	time.Sleep(300 * time.Millisecond)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(p.requests))
	}
	var seen []string
	for _, m := range p.requests[0].Messages {
		seen = append(seen, m.PlainText())
	}
	all := strings.Join(seen, "\n")
	for _, want := range []string{"任务 A", "任务 B", "任务 C"} {
		if !strings.Contains(all, want) {
			t.Fatalf("the turn did not see %q: %s", want, all)
		}
	}
	if last := msgs[len(msgs)-1]; last.Role != "assistant" || last.ReplyTo != msgs[2].Seq {
		t.Fatalf("the reply covers %d, want %d", last.ReplyTo, msgs[2].Seq)
	}
}

// Triggers alone cannot keep the supervisor going forever: after
// MaxAutoTurns it stops, tells the user once, and a user message resets it.
func TestAutoTurnsStop(t *testing.T) {
	p := &scripted{responses: replies(MaxAutoTurns+2, "接着做")}
	log, main := setupBatch(t, p, -1)
	n := 0
	for i := 0; i < MaxAutoTurns; i++ {
		taskNews(t, log, main, fmt.Sprintf("k%d", i), fmt.Sprintf("任务 %d 完成了", i))
		n += 2
		waitMessages(t, log, main, n)
	}
	taskNews(t, log, main, "over", "又一个任务完成了")
	n += 2 // the trigger, a silent reply
	n++    // the notice, in the main thread too
	msgs := waitMessages(t, log, main, n)
	p.mu.Lock()
	calls := len(p.requests)
	p.mu.Unlock()
	if calls != MaxAutoTurns {
		t.Fatalf("model calls = %d, want %d", calls, MaxAutoTurns)
	}
	var notices int
	for _, m := range msgs {
		if m.Kind == event.KindNotice && strings.Contains(m.Content, "先停下来") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("notices = %d: %+v", notices, msgs)
	}
	taskNews(t, log, main, "over2", "再一个")
	n += 2
	waitMessages(t, log, main, n)
	say(t, log, main, "继续")
	n += 2
	waitMessages(t, log, main, n)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != MaxAutoTurns+1 {
		t.Fatalf("after the user wrote: model calls = %d, want %d", len(p.requests), MaxAutoTurns+1)
	}
}

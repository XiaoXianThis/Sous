package supervisor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func callOf(name, args string) provider.Response {
	return provider.Response{Message: provider.Message{Role: provider.RoleAssistant,
		Parts: []provider.Part{{ToolCall: &provider.ToolCall{ID: name, Name: name, Args: json.RawMessage(args)}}}}}
}

func textOf(s string) provider.Response {
	return provider.Response{Message: provider.Text(provider.RoleAssistant, s)}
}

// lastText is everything in a request's last message, tool results included.
func lastText(req provider.Request) string {
	m := req.Messages[len(req.Messages)-1]
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text)
		if p.ToolResult != nil {
			b.WriteString(p.ToolResult.Content)
		}
	}
	return b.String()
}

// recorder is a tool that counts its calls and runs a hook when called.
type recorder struct {
	name string
	mu   sync.Mutex
	n    int
	hook func()
}

func (r *recorder) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: r.name, Description: r.name, Schema: json.RawMessage(`{"type":"object"}`)}
}

func (r *recorder) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	r.mu.Lock()
	r.n++
	hook := r.hook
	r.hook = nil
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	return "ok", nil
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

func messageByID(t *testing.T, log *eventlog.Log, thread, id string) store.Message {
	t.Helper()
	m, err := log.Store.Message(context.Background(), thread, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// waitFor polls cond for up to five seconds.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func repliesTo(log *eventlog.Log, thread string) []store.Message {
	msgs, _ := log.Store.MessagesAfter(context.Background(), thread, 0)
	var out []store.Message
	for _, m := range msgs {
		if m.Role == "assistant" && m.Kind != event.KindStep {
			out = append(out, m)
		}
	}
	return out
}

// A message from the main input starts a topic; each topic gets its own
// turn, oldest waiting first, and a turn does not see the input other
// topics are waiting with.
func TestTopicsGetSeparateTurns(t *testing.T) {
	p := &scripted{block: make(chan struct{}), responses: []provider.Response{textOf("答 a"), textOf("答 b"), textOf("答 c")}}
	log, main, _ := setup(t, p, nil)
	say(t, log, main, "a")
	time.Sleep(50 * time.Millisecond) // the turn on a is in the model call
	say(t, log, main, "b")
	sayIn(t, log, main, "a", "c")
	for range 3 {
		p.block <- struct{}{}
	}
	waitUntil(t, "three replies", func() bool { return len(repliesTo(log, main)) == 3 })
	rs := repliesTo(log, main)
	if rs[0].TopicID != "a" || rs[1].TopicID != "b" || rs[2].TopicID != "a" {
		t.Fatalf("reply topics: %s %s %s", rs[0].TopicID, rs[1].TopicID, rs[2].TopicID)
	}
	if c := messageByID(t, log, main, "c"); rs[2].ReplyTo != c.Seq {
		t.Fatalf("the third turn answers c: %+v", rs[2])
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.requests[1].Messages {
		if said(m) == "c" {
			t.Fatal("the turn on b saw c, which waits in topic a")
		}
	}
	if !strings.Contains(lastText(p.requests[1]), "本轮的话题：【话题#") {
		t.Fatalf("the tail names the topic: %s", lastText(p.requests[1]))
	}
	topic, _ := log.Store.Topic(context.Background(), "b")
	if topic.Title != "b" || topic.Status != event.TopicOpen {
		t.Fatalf("topic b: %+v", topic)
	}
}

// A follow-up in the turn's topic is taken into the turn before the next
// model call, and the turn's reply answers it too.
func TestFollowUpIsTakenIn(t *testing.T) {
	var log *eventlog.Log
	var main string
	look := &recorder{name: "task_list"}
	p := &scripted{responses: []provider.Response{callOf("task_list", `{}`), textOf("改好了")}}
	log, main, _ = setup(t, p, tool.NewRegistry(look))
	look.hook = func() { sayIn(t, log, main, "a", "用 b 方案") }
	say(t, log, main, "a")
	waitUntil(t, "a reply", func() bool { return len(repliesTo(log, main)) == 1 })
	time.Sleep(100 * time.Millisecond)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 2 {
		t.Fatalf("model calls = %d, want 2 (no extra turn)", len(p.requests))
	}
	if got := lastText(p.requests[1]); !strings.Contains(got, "用户在这个话题里补充了一条消息") || !strings.Contains(got, "用 b 方案") {
		t.Fatalf("the follow-up is not in the request: %s", got)
	}
	if r := repliesTo(log, main)[0]; r.ReplyTo != messageByID(t, log, main, "用 b 方案").Seq {
		t.Fatalf("the reply does not answer the follow-up: %+v", r)
	}
}

// A message that starts a topic during a turn is offered as a possible
// follow-up; topic_attach moves it into the turn's topic.
func TestNewMessageAttached(t *testing.T) {
	var log *eventlog.Log
	var main string
	look := &recorder{name: "task_list"}
	p := &scripted{responses: []provider.Response{callOf("task_list", `{}`), callOf("topic_attach", `{"message_id":"顺便改下标题"}`), textOf("都改好了")}}
	log, main, _ = setup(t, p, tool.NewRegistry(look))
	look.hook = func() { say(t, log, main, "顺便改下标题") }
	say(t, log, main, "a")
	waitUntil(t, "a reply", func() bool { return len(repliesTo(log, main)) == 1 })
	time.Sleep(100 * time.Millisecond)
	m := messageByID(t, log, main, "顺便改下标题")
	if m.TopicID != "a" {
		t.Fatalf("not attached: %+v", m)
	}
	if r := repliesTo(log, main); len(r) != 1 || r[0].ReplyTo != m.Seq {
		t.Fatalf("replies: %+v", r)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 3 || !strings.Contains(lastText(p.requests[1]), "message_id 顺便改下标题") {
		t.Fatalf("requests: %d %s", len(p.requests), lastText(p.requests[1]))
	}
}

// A new message the supervisor does not attach waits for its own turn.
func TestNewMessageNotAttachedGetsItsOwnTurn(t *testing.T) {
	var log *eventlog.Log
	var main string
	look := &recorder{name: "task_list"}
	p := &scripted{responses: []provider.Response{callOf("task_list", `{}`), textOf("a 好了"), textOf("另一件事也好了")}}
	log, main, _ = setup(t, p, tool.NewRegistry(look))
	look.hook = func() { say(t, log, main, "另一件事") }
	say(t, log, main, "a")
	waitUntil(t, "two replies", func() bool { return len(repliesTo(log, main)) == 2 })
	rs := repliesTo(log, main)
	if rs[0].TopicID != "a" || rs[1].TopicID != "另一件事" {
		t.Fatalf("replies: %+v", rs)
	}
}

// Tools that change something wait when the user wrote while the model
// was deciding; the model sees the message and decides again.
func TestSideEffectsWaitForNewMessage(t *testing.T) {
	act := &recorder{name: "task_create"}
	var log *eventlog.Log
	var main string
	p := &answering{}
	p.fn = func(req provider.Request, n int) (provider.Response, error) {
		switch n {
		case 0:
			sayIn(t, log, main, "a", "等等，换成 Go")
			return callOf("task_create", `{}`), nil
		case 1:
			return callOf("task_create", `{"lang":"go"}`), nil
		}
		return textOf("已派发"), nil
	}
	log, main = setupWith(t, Options{Provider: p, Tools: tool.NewRegistry(act)})
	say(t, log, main, "a")
	waitUntil(t, "a reply", func() bool { return len(repliesTo(log, main)) == 1 })
	if act.calls() != 1 {
		t.Fatalf("task_create ran %d times, want 1 (the held call must not run)", act.calls())
	}
	reqs := p.all()
	got := lastText(reqs[1])
	if !strings.Contains(got, "未执行") || !strings.Contains(got, "换成 Go") {
		t.Fatalf("second request: %s", got)
	}
}

// Quick messages from the main input before a turn starts are offered to
// it as possible follow-ups.
func TestQuickMessagesAreOffered(t *testing.T) {
	p := &answering{}
	p.fn = func(req provider.Request, n int) (provider.Response, error) {
		if n == 0 {
			return callOf("topic_attach", `{"message_id":"还有 y"}`), nil
		}
		return textOf("x 和 y 都记下了"), nil
	}
	log, main := setupWith(t, Options{Provider: p, Gather: 150 * time.Millisecond})
	say(t, log, main, "做 x")
	say(t, log, main, "还有 y")
	waitUntil(t, "a reply", func() bool { return len(repliesTo(log, main)) == 1 })
	time.Sleep(100 * time.Millisecond)
	reqs := p.all()
	if len(reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(reqs))
	}
	if got := lastText(reqs[0]); !strings.Contains(got, "做 x") || !strings.Contains(got, "message_id 还有 y") {
		t.Fatalf("first request: %s", got)
	}
	if m := messageByID(t, log, main, "还有 y"); m.TopicID != "做 x" {
		t.Fatalf("y: %+v", m)
	}
}

// topic_update keeps the current conclusion; topic_close closes the topic,
// and a new message reopens it.
func TestTopicToolsAndReopen(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		callOf("topic_update", `{"summary":"已修复，等你确认","title":"修登录"}`),
		callOf("topic_close", `{"summary":"登录修好了"}`),
		textOf("好了"),
		textOf("再看看"),
	}}
	log, main, _ := setup(t, p, nil)
	ctx := context.Background()
	say(t, log, main, "登录坏了")
	waitUntil(t, "a reply", func() bool { return len(repliesTo(log, main)) == 1 })
	topic, _ := log.Store.Topic(ctx, "登录坏了")
	if topic.Title != "修登录" || topic.Summary != "登录修好了" || topic.Status != event.TopicClosed {
		t.Fatalf("topic: %+v", topic)
	}
	sayIn(t, log, main, "登录坏了", "又坏了")
	waitUntil(t, "a second reply", func() bool { return len(repliesTo(log, main)) == 2 })
	topic, _ = log.Store.Topic(ctx, "登录坏了")
	if topic.Status != event.TopicOpen {
		t.Fatalf("not reopened: %+v", topic)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.Contains(lastText(p.requests[3]), "当前结论：登录修好了") {
		t.Fatalf("tail: %s", lastText(p.requests[3]))
	}
}

// topic_attach only takes messages offered in the turn.
func TestAttachRefusesOtherMessages(t *testing.T) {
	p := &scripted{responses: []provider.Response{callOf("topic_attach", `{"message_id":"old"}`), textOf("好"), textOf("新的也好")}}
	log, main, _ := setup(t, p, nil)
	say(t, log, main, "old")
	waitUntil(t, "a reply", func() bool { return len(repliesTo(log, main)) == 1 })
	say(t, log, main, "new")
	waitUntil(t, "a second reply", func() bool { return len(repliesTo(log, main)) == 2 })
	if m := messageByID(t, log, main, "old"); m.TopicID != "old" {
		t.Fatalf("old moved: %+v", m)
	}
}

func TestPendingTopics(t *testing.T) {
	msgs := []store.Message{
		{ID: "a", Role: "user", Seq: 1, TopicID: "a"},
		{ID: "b", Role: "user", Seq: 2, TopicID: "b"},
		{ID: "r", Role: "assistant", Seq: 3, TopicID: "a", ReplyTo: 1},
		{ID: "c", Role: "user", Seq: 4, TopicID: "a"},
		{ID: "old", Role: "user", Seq: 5},
		{ID: "r2", Role: "assistant", Seq: 6, ReplyTo: 5},
	}
	p := pendingTopics(msgs)
	if len(p) != 2 || p[0].ID != "b" || p[1].ID != "a" || p[1].Newest != 4 {
		t.Fatalf("pending: %+v", p)
	}
	if unanswered(msgs) != 2 || oldestPending(msgs) != 2 {
		t.Fatalf("unanswered %d oldest %d", unanswered(msgs), oldestPending(msgs))
	}
	turn := forTopic(msgs, "b")
	for _, m := range turn {
		if m.ID == "c" {
			t.Fatal("c waits in another topic")
		}
	}
	// A reply written without a topic answers the message it names.
	legacy := []store.Message{{ID: "x", Role: "user", Seq: 1, TopicID: "x"}, {ID: "rx", Role: "assistant", Seq: 2, ReplyTo: 1}}
	if len(pendingTopics(legacy)) != 0 {
		t.Fatal("legacy reply not counted")
	}
}

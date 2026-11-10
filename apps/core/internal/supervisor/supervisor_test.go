package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
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

// scripted returns canned responses in order and records each request.
type scripted struct {
	mu        sync.Mutex
	responses []provider.Response
	errs      []error
	requests  []provider.Request
	block     chan struct{}
}

func (s *scripted) Name() string { return "scripted" }
func (s *scripted) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := len(s.requests)
	s.requests = append(s.requests, req)
	if i < len(s.errs) && s.errs[i] != nil {
		return provider.Response{}, s.errs[i]
	}
	r := s.responses[i]
	if t := r.Message.PlainText(); t != "" && onDelta != nil {
		onDelta(provider.Delta{Text: t})
	}
	return r, nil
}

func setup(t *testing.T, p provider.Provider, tools *tool.Registry) (*eventlog.Log, string, *Supervisor) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.New(st, event.NewBus())
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(Options{Log: log, Provider: p, Model: "m", Tools: tools})
	sup.Start(ctx)
	t.Cleanup(func() { cancel(); sup.Wait(); st.Close() })
	main, err := log.EnsureMainThread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return log, main, sup
}

// said is the text of a message without the turn's environment tail and
// the topic label.
func said(m provider.Message) string {
	if len(m.Parts) == 0 {
		return ""
	}
	return topicMark.ReplaceAllString(m.Parts[0].Text, "")
}

var topicMark = regexp.MustCompile(`【话题#\d+】`)

func say(t *testing.T, log *eventlog.Log, thread, text string) {
	t.Helper()
	if _, err := log.Append(context.Background(), event.New(event.MessageUser, event.ActorUser, thread,
		event.MessagePayload{MessageID: text, Content: text})); err != nil {
		t.Fatal(err)
	}
}

func waitMessages(t *testing.T, log *eventlog.Log, thread string, n int) []store.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _ := log.Store.Messages(context.Background(), thread, 0, 100)
		if len(msgs) >= n {
			return msgs
		}
		time.Sleep(10 * time.Millisecond)
	}
	msgs, _ := log.Store.Messages(context.Background(), thread, 0, 100)
	t.Fatalf("timed out waiting for %d messages, have %+v", n, msgs)
	return nil
}

func TestReplyStreamsAndStores(t *testing.T) {
	p := &scripted{responses: []provider.Response{{Message: provider.Text(provider.RoleAssistant, "你好，我在"), Usage: provider.Usage{InputTokens: 5, OutputTokens: 3}}}}
	log, main, _ := setup(t, p, nil)
	deltas, stop := log.Bus.Subscribe(100)
	defer stop()

	say(t, log, main, "在吗")
	msgs := waitMessages(t, log, main, 2)
	if msgs[1].Role != "assistant" || msgs[1].Content != "你好，我在" {
		t.Fatalf("reply: %+v", msgs[1])
	}
	gotDelta := false
	for len(deltas) > 0 {
		if ev := <-deltas; ev.Type == event.MessageAssistantDelta && ev.Seq == 0 {
			var d event.DeltaPayload
			ev.Decode(&d)
			gotDelta = gotDelta || (d.MessageID == msgs[1].ID && d.Delta == "你好，我在")
		}
	}
	if !gotDelta {
		t.Fatal("no delta with the reply's message id was published")
	}
	if !strings.Contains(p.requests[0].System, "sous") || said(p.requests[0].Messages[0]) != "在吗" {
		t.Fatalf("request: %+v", p.requests[0])
	}
	usage, _ := log.Store.UsageSince(context.Background(), time.Now().Add(-time.Minute))
	if len(usage) != 1 || usage[0].InputTokens != 5 {
		t.Fatalf("usage: %+v", usage)
	}
}

func TestToolLoop(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{{ToolCall: &provider.ToolCall{ID: "c1", Name: "current_time"}}}}},
		{Message: provider.Text(provider.RoleAssistant, "现在十点")},
	}}
	fixed := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	log, main, _ := setup(t, p, tool.NewRegistry(tool.CurrentTime{Now: func() time.Time { return fixed }}))
	say(t, log, main, "几点了")
	msgs := waitMessages(t, log, main, 2)
	if msgs[1].Content != "现在十点" {
		t.Fatalf("reply: %q", msgs[1].Content)
	}
	second := p.requests[1].Messages
	last := second[len(second)-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Parts[0].ToolResult.Content, "2026-11-03 10:00") {
		t.Fatalf("tool result not sent back: %+v", last)
	}
}

func TestModelErrorIsReportedAndHiddenFromHistory(t *testing.T) {
	p := &scripted{
		errs:      []error{errors.New("boom"), nil},
		responses: []provider.Response{{}, {Message: provider.Text(provider.RoleAssistant, "好了")}},
	}
	log, main, _ := setup(t, p, nil)
	say(t, log, main, "第一句")
	msgs := waitMessages(t, log, main, 2)
	if !msgs[1].Error || !strings.Contains(msgs[1].Content, "boom") {
		t.Fatalf("error reply: %+v", msgs[1])
	}
	say(t, log, main, "第二句")
	waitMessages(t, log, main, 4)
	for _, m := range p.requests[1].Messages {
		if strings.Contains(m.PlainText(), "boom") {
			t.Fatal("error reply leaked into the model's history")
		}
	}
}

// sayIn writes a user message into a topic.
func sayIn(t *testing.T, log *eventlog.Log, thread, topic, text string) {
	t.Helper()
	if _, err := log.Append(context.Background(), event.New(event.MessageUser, event.ActorUser, thread,
		event.MessagePayload{MessageID: text, Content: text, TopicID: topic})); err != nil {
		t.Fatal(err)
	}
}

// Follow-ups in a topic that arrive after a turn's last model call are one
// next turn.
func TestMessagesDuringTurnAreBatched(t *testing.T) {
	p := &scripted{block: make(chan struct{}), responses: []provider.Response{
		{Message: provider.Text(provider.RoleAssistant, "一")},
		{Message: provider.Text(provider.RoleAssistant, "二")},
	}}
	log, main, _ := setup(t, p, nil)
	say(t, log, main, "a")
	time.Sleep(50 * time.Millisecond) // first turn is now blocked in the model call
	sayIn(t, log, main, "a", "b")
	sayIn(t, log, main, "a", "c")
	p.block <- struct{}{}
	p.block <- struct{}{}
	msgs := waitMessages(t, log, main, 5)
	if len(msgs) != 5 {
		t.Fatalf("messages: %+v", msgs)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 2 {
		t.Fatalf("expected b and c in one turn, got %d model calls", len(p.requests))
	}
	got := p.requests[1].Messages
	if said(got[len(got)-1]) != "c" || said(got[len(got)-2]) != "b" {
		t.Fatalf("second turn input: %+v", got)
	}
}

func TestMockProviderEndToEnd(t *testing.T) {
	p, _ := provider.New(provider.Config{Name: "mock", Kind: "mock"})
	log, main, _ := setup(t, p, tool.NewRegistry(tool.CurrentTime{}))
	say(t, log, main, "现在几点")
	msgs := waitMessages(t, log, main, 2)
	if !strings.Contains(msgs[1].Content, "现在是") {
		t.Fatalf("reply: %q", msgs[1].Content)
	}
}

func TestStaySilent(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{{ToolCall: &provider.ToolCall{ID: "s", Name: "stay_silent", Args: []byte(`{"reason":"用户只是道谢"}`)}}}}},
		{Message: provider.Text(provider.RoleAssistant, "收到")},
	}}
	log, main, _ := setup(t, p, nil)
	say(t, log, main, "谢谢")
	msgs := waitMessages(t, log, main, 2)
	if msgs[1].Kind != event.KindSilent || msgs[1].Content != "用户只是道谢" || msgs[1].ReplyTo != msgs[0].Seq {
		t.Fatalf("silent turn: %+v", msgs[1])
	}
	// The silent turn counts as handled, and is hidden from the model.
	say(t, log, main, "下一句")
	waitMessages(t, log, main, 4)
	for _, m := range p.requests[1].Messages {
		if m.PlainText() == "用户只是道谢" {
			t.Fatal("silent reason leaked into history")
		}
	}
	if len(p.requests) != 2 {
		t.Fatalf("model calls: %d", len(p.requests))
	}
}

func TestSilentWithTextIsRefused(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{
			{Text: "好的"},
			{ToolCall: &provider.ToolCall{ID: "s", Name: "stay_silent", Args: []byte(`{}`)}},
		}}},
		{Message: provider.Text(provider.RoleAssistant, "已记下")},
	}}
	log, main, _ := setup(t, p, nil)
	say(t, log, main, "记一下")
	msgs := waitMessages(t, log, main, 3)
	if msgs[1].Kind != event.KindStep || msgs[1].Content != "好的" || msgs[2].Kind != "" || !strings.Contains(msgs[2].Content, "已记下") {
		t.Fatalf("replies: %+v", msgs[1:])
	}
	res := p.requests[1].Messages[len(p.requests[1].Messages)-1].Parts[0].ToolResult
	if res == nil || !res.IsError {
		t.Fatalf("stay_silent with text should be refused: %+v", res)
	}
}

func TestRelevantMemoriesReachThePrompt(t *testing.T) {
	p := &scripted{responses: []provider.Response{{Message: provider.Text(provider.RoleAssistant, "好")}}}
	log, main, _ := setup(t, p, nil)
	ctx := context.Background()
	log.Append(ctx,
		event.New(event.MemoryPut, "user", "", event.MemoryPayload{MemoryID: "p1", Kind: "profile", Content: "用户叫小贤"}),
		event.New(event.MemoryPut, "user", "", event.MemoryPayload{MemoryID: "j1", Kind: "project", Content: "sous 的 Hub 部署在 agent.wxian.cc"}),
		event.New(event.MemoryPut, "user", "", event.MemoryPayload{MemoryID: "x1", Kind: "note", Content: "周末去爬山"}),
	)
	say(t, log, main, "Hub 部署在哪里？")
	waitMessages(t, log, main, 2)
	msgs := p.requests[0].Messages
	head, tail := msgs[0].PlainText(), msgs[len(msgs)-1].PlainText()
	if !strings.Contains(head, "[p1] (profile) 用户叫小贤") || !strings.Contains(tail, "[j1] (project)") {
		t.Fatalf("missing memories:\nhead: %s\ntail: %s", head, tail)
	}
	if strings.Contains(head+tail, "爬山") || strings.Contains(tail, "[p1]") {
		t.Fatalf("unrelated or repeated memory included:\nhead: %s\ntail: %s", head, tail)
	}
}

func TestTriggerStartsATurn(t *testing.T) {
	p := &scripted{responses: []provider.Response{{Message: provider.Text(provider.RoleAssistant, "昨天的进展如下。")}}}
	log, main, _ := setup(t, p, nil)
	ev := event.New(event.TriggerFired, event.ActorTrigger, main, event.TriggerPayload{
		Kind: event.TriggerCron, Key: "schedule:s1", ScheduleID: "", MessageID: "m1", Title: "日报", Content: "【定时任务「日报」】\n汇总昨天的进展",
	})
	if _, err := log.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	msgs := waitMessages(t, log, main, 2)
	if msgs[0].Kind != event.KindTrigger || msgs[0].Role != "user" || msgs[1].Content != "昨天的进展如下。" || msgs[1].ReplyTo != msgs[0].Seq {
		t.Fatalf("messages: %+v", msgs)
	}
	if got := p.requests[0].Messages[0].PlainText(); !strings.Contains(got, "汇总昨天的进展") {
		t.Fatalf("the model saw %q", got)
	}
	if !strings.Contains(p.requests[0].System, "【跟进】") {
		t.Fatal("the prompt does not explain triggers")
	}
}

// probeTool records the thread's messages at the moment it runs.
type probeTool struct {
	log  *eventlog.Log
	seen []store.Message
}

func (p *probeTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: "probe", Description: "test", Schema: json.RawMessage(`{"type":"object"}`)}
}

func (p *probeTool) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	th, _ := p.log.Store.MainThreadID(ctx)
	p.seen, _ = p.log.Store.Messages(ctx, th, 0, 50)
	return "ok", nil
}

// What the model says before calling tools is stored before they run, so
// whatever the tools add to the thread comes after it; the step does not
// answer the user, the turn's last message does, and the model sees the
// whole turn as one message.
func TestStepsKeepTheOrderOfWordsAndTools(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{
			{Text: "我先建任务"},
			{ToolCall: &provider.ToolCall{ID: "c1", Name: "probe", Args: []byte(`{}`)}},
		}}},
		{Message: provider.Text(provider.RoleAssistant, "建好了")},
		{Message: provider.Text(provider.RoleAssistant, "好")},
	}}
	probe := &probeTool{}
	tools := tool.NewRegistry()
	tools.Register(probe)
	log, main, _ := setup(t, p, tools)
	probe.log = log
	say(t, log, main, "建三个任务")
	msgs := waitMessages(t, log, main, 3)
	if msgs[1].Kind != event.KindStep || msgs[1].Content != "我先建任务" || msgs[2].Content != "建好了" || msgs[2].Kind != "" {
		t.Fatalf("messages: %+v", msgs)
	}
	if n := len(probe.seen); n != 2 || probe.seen[1].Kind != event.KindStep {
		t.Fatalf("the step should be stored before the tool runs: %+v", probe.seen)
	}
	if got := unanswered(msgs[:2]); got != msgs[0].Seq {
		t.Fatalf("a step must not answer the user message: %d", got)
	}
	say(t, log, main, "谢谢")
	waitMessages(t, log, main, 5)
	var assistant []string
	for _, m := range p.requests[2].Messages {
		if m.Role == provider.RoleAssistant {
			assistant = append(assistant, m.PlainText())
		}
	}
	if len(assistant) != 1 || assistant[0] != "我先建任务\n【本轮工具】probe → ok\n\n建好了" {
		t.Fatalf("history: %q", assistant)
	}
}

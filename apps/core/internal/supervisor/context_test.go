package supervisor

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// answering replies to every call with fn's answer and records the requests.
type answering struct {
	mu       sync.Mutex
	requests []provider.Request
	fn       func(req provider.Request, n int) (provider.Response, error)
}

func (a *answering) Name() string { return "answering" }
func (a *answering) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	a.mu.Lock()
	n := len(a.requests)
	a.requests = append(a.requests, req)
	a.mu.Unlock()
	return a.fn(req, n)
}

func (a *answering) all() []provider.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]provider.Request(nil), a.requests...)
}

func reply(text string) func(provider.Request, int) (provider.Response, error) {
	return func(provider.Request, int) (provider.Response, error) {
		return provider.Response{Message: provider.Text(provider.RoleAssistant, text)}, nil
	}
}

func setupWith(t *testing.T, opt Options) (*eventlog.Log, string) {
	log, main, _ := setupSup(t, opt)
	return log, main
}

func setupSup(t *testing.T, opt Options) (*eventlog.Log, string, *Supervisor) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.New(st, event.NewBus())
	opt.Log = log
	if opt.Model == "" {
		opt.Model = "m"
	}
	ctx, cancel := context.WithCancel(context.Background())
	sup := New(opt)
	sup.Start(ctx)
	t.Cleanup(func() { cancel(); sup.Wait(); st.Close() })
	main, err := log.EnsureMainThread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return log, main, sup
}

// converse says each text in turn, waiting for its reply.
func converse(t *testing.T, log *eventlog.Log, thread string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		before, _ := log.Store.MessagesAfter(context.Background(), thread, 0)
		say(t, log, thread, text)
		deadline := time.Now().Add(5 * time.Second)
		for {
			msgs, _ := log.Store.MessagesAfter(context.Background(), thread, 0)
			if len(msgs) >= len(before)+2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no reply to %q", text)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func idle(t *testing.T, log *eventlog.Log, thread string) {
	t.Helper()
	// The worker compacts after the reply is stored; give it a moment.
	time.Sleep(100 * time.Millisecond)
}

// Within a segment each request repeats the previous one and only adds at
// the end: the system prompt, tools and every earlier message are the same,
// and only the newest message carries the turn's environment.
func TestRequestsOnlyGrowWithinASegment(t *testing.T) {
	p := &answering{fn: reply("好的")}
	log, main := setupWith(t, Options{Provider: p})
	ctx := context.Background()
	log.Append(ctx, event.New(event.MemoryPut, "user", "", event.MemoryPayload{MemoryID: "p1", Kind: "profile", Content: "用户叫小贤"}))
	converse(t, log, main, "第一句", "第二句")
	log.Append(ctx, event.New(event.MemoryPut, "user", "", event.MemoryPayload{MemoryID: "p2", Kind: "profile", Content: "用户喜欢简洁"}))
	converse(t, log, main, "第三句")

	reqs := p.all()
	if len(reqs) != 3 {
		t.Fatalf("%d calls", len(reqs))
	}
	for i := 1; i < len(reqs); i++ {
		prev, cur := reqs[i-1], reqs[i]
		if prev.System != cur.System || !reflect.DeepEqual(prev.Tools, cur.Tools) {
			t.Fatalf("turn %d changed the system prompt or tools", i)
		}
		last := len(prev.Messages) - 1
		for j := 0; j < last; j++ {
			if !reflect.DeepEqual(prev.Messages[j], cur.Messages[j]) {
				t.Fatalf("turn %d changed message %d:\n%+v\n%+v", i, j, prev.Messages[j], cur.Messages[j])
			}
		}
		if said(prev.Messages[last]) != said(cur.Messages[last]) || len(cur.Messages[last].Parts) != 1 {
			t.Fatalf("turn %d: the previous newest message should lose only its tail: %+v", i, cur.Messages[last])
		}
	}
	if strings.Contains(reqs[0].System, "当前时间") {
		t.Fatal("the system prompt holds the time")
	}
	head := reqs[0].Messages[0].PlainText()
	if !strings.Contains(head, "用户叫小贤") {
		t.Fatalf("head: %s", head)
	}
	// A profile memory saved mid-segment shows in the tail, not the head.
	lastReq := reqs[2]
	tail := lastReq.Messages[len(lastReq.Messages)-1].PlainText()
	if strings.Contains(lastReq.Messages[0].PlainText(), "简洁") || !strings.Contains(tail, "[p2] (profile) 用户喜欢简洁") ||
		!strings.Contains(tail, "当前时间") {
		t.Fatalf("tail: %s", tail)
	}
	if got := lastReq.CacheAt; len(got) != 3 || got[0] != 0 || got[2] != len(lastReq.Messages)-1 {
		t.Fatalf("cache breakpoints: %v", got)
	}
}

// long is a message of about n tokens.
func long(tag string, n int) string {
	return tag + strings.Repeat("长", n)
}

// Once a thread passes the soft threshold, the older messages are folded
// into a summary; the next turn shows the summary and only recent messages.
func TestCompactionFoldsOldMessages(t *testing.T) {
	sup := &answering{fn: reply("嗯")}
	comp := &answering{fn: reply("## 已做的决定\n- 部署在 agent.wxian.cc [#3]\n## 待办和承诺\n- 无")}
	// The window leaves room for the fixed part (system prompt and tools,
	// about 5k tokens with the topic tools) next to the kept messages.
	log, main := setupWith(t, Options{Provider: sup, Compactor: comp, CompactorModel: "c",
		ContextWindow: func(context.Context) int { return 16000 }})
	for i := 0; i < 18; i++ {
		converse(t, log, main, long(string(rune('A'+i)), 300))
	}
	idle(t, log, main)
	ctx := context.Background()
	seg, err := log.Store.LatestSegment(ctx, main)
	if err != nil || seg.Segment < 2 || seg.CoversUpTo == 0 || seg.Reason != event.CompactSoft || !strings.Contains(seg.Summary, "agent.wxian.cc") {
		t.Fatalf("segment: %+v %v", seg, err)
	}
	creq := comp.all()
	if len(creq) == 0 || !strings.Contains(creq[0].Messages[0].PlainText(), "[#") || !strings.Contains(creq[0].Messages[0].PlainText(), "A长") {
		t.Fatalf("compactor input: %+v", creq)
	}
	converse(t, log, main, "最后一句")
	reqs := sup.all()
	last := reqs[len(reqs)-1]
	if !strings.Contains(last.Messages[0].PlainText(), "agent.wxian.cc") {
		t.Fatalf("the summary is not in the head: %s", last.Messages[0].PlainText())
	}
	for _, m := range last.Messages[1:] {
		if strings.HasPrefix(said(m), "A长") {
			t.Fatal("a folded message is still sent")
		}
	}
	if n := estimateRequest(last); n > int(SoftRatio*16000) {
		t.Fatalf("after compaction the request is still %d tokens", n)
	}
}

// Without a compactor the oldest messages are left out to fit the window.
func TestTrimWithoutCompactor(t *testing.T) {
	sup := &answering{fn: reply("嗯")}
	log, main := setupWith(t, Options{Provider: sup, ContextWindow: func(context.Context) int { return 6000 }})
	for i := 0; i < 16; i++ {
		converse(t, log, main, long(string(rune('A'+i)), 300))
	}
	reqs := sup.all()
	last := reqs[len(reqs)-1]
	if n := estimateRequest(last); n > int(HardRatio*6000) {
		t.Fatalf("request of %d tokens", n)
	}
	if !strings.HasPrefix(said(last.Messages[len(last.Messages)-1]), "P长") {
		t.Fatal("the newest message is missing")
	}
}

// A context-overflow error compacts the thread and retries once.
func TestOverflowCompactsAndRetries(t *testing.T) {
	overflowed := false
	sup := &answering{fn: func(req provider.Request, n int) (provider.Response, error) {
		if said(req.Messages[len(req.Messages)-1]) == "溢出" && !overflowed {
			overflowed = true
			return provider.Response{}, &provider.APIError{Status: 400, Body: `{"error":{"message":"This model's maximum context length is 65536 tokens"}}`}
		}
		return provider.Response{Message: provider.Text(provider.RoleAssistant, "嗯")}, nil
	}}
	comp := &answering{fn: reply("## 待办和承诺\n- 无")}
	log, main := setupWith(t, Options{Provider: sup, Compactor: comp, CompactorModel: "c"})
	for i := 0; i < 12; i++ {
		converse(t, log, main, "第"+string(rune('A'+i))+"句")
	}
	converse(t, log, main, "溢出")
	msgs, _ := log.Store.MessagesAfter(context.Background(), main, 0)
	if last := msgs[len(msgs)-1]; last.Content != "嗯" || last.Error {
		t.Fatalf("reply: %+v", last)
	}
	seg, _ := log.Store.LatestSegment(context.Background(), main)
	if seg.Reason != event.CompactOverflow || len(comp.all()) == 0 {
		t.Fatalf("segment: %+v", seg)
	}
}

// A summary that drops a commitment gets one retry; if the commitment is
// still missing it is put back.
func TestSummaryKeepsCommitments(t *testing.T) {
	old := "## 已做的决定\n- 用 Caddy 反代 [#3]\n## 待办和承诺\n- 周五提醒用户续费域名 [#7]\n## 已完成或作废\n- 无"
	dropped := "## 已做的决定\n- 用 Caddy 反代 [#3]\n## 待办和承诺\n- 无\n## 进行中的话题\n- 聊多机同步 [#20]"
	comp := &answering{fn: reply(dropped)}
	_, main, s := setupSup(t, Options{Provider: &answering{fn: reply("嗯")}, Compactor: comp, CompactorModel: "c"})
	got, err := s.summarize(context.Background(), main, old, []store.Message{{Role: "user", Content: "聊聊多机同步", Seq: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if len(comp.all()) != 2 {
		t.Fatalf("want one retry, got %d calls", len(comp.all()))
	}
	if !strings.Contains(got, "续费域名 [#7]") || !strings.Contains(got, "聊多机同步") {
		t.Fatalf("summary:\n%s", got)
	}
	items := parseSummary(got)
	for _, it := range items {
		if strings.Contains(it.text, "续费") && it.section != "待办和承诺" {
			t.Fatalf("put back into %q", it.section)
		}
	}

	// Moving an item to the done section is fine.
	done := "## 已做的决定\n- 用 Caddy 反代 [#3]\n## 待办和承诺\n- 无\n## 已完成或作废\n- 已提醒续费域名 [#7][#30]"
	if m := missingItems(old, done); len(m) != 0 {
		t.Fatalf("missing: %+v", m)
	}
}

// Old messages are clipped to their head and tail; the ones being answered
// stay whole.
func TestClipLongMessages(t *testing.T) {
	big := strings.Repeat("字", BigMessageRunes+100)
	msgs := []store.Message{
		{Role: "user", Content: big, Seq: 1},
		{Role: "assistant", Content: "好", Seq: 2, ReplyTo: 1},
		{Role: "user", Content: big, Seq: 3},
	}
	got := clipAnswered(msgs)
	if !strings.Contains(got[0].Content, "thread_read #1") || len([]rune(got[0].Content)) > 2*bigMessageKeep+50 {
		t.Fatalf("old message not clipped: %d runes", len([]rune(got[0].Content)))
	}
	if got[2].Content != big {
		t.Fatal("the unanswered message was clipped")
	}
}

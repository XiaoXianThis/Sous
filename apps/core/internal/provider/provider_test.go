package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer streams the given SSE lines and records the last request.
type fakeServer struct {
	*httptest.Server
	path   string
	header http.Header
	body   map[string]any
}

func newFake(t *testing.T, status int, sse string) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path = r.URL.Path
		f.header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &f.body)
		if status != 200 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":"nope"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	t.Cleanup(f.Close)
	return f
}

func conversation() Request {
	return Request{
		Model:  "m",
		System: "你是秘书",
		Messages: []Message{
			Text(RoleUser, "几点了"),
			{Role: RoleAssistant, Parts: []Part{{ToolCall: &ToolCall{ID: "c1", Name: "current_time", Args: json.RawMessage(`{}`)}}}},
			{Role: RoleTool, Parts: []Part{{ToolResult: &ToolResult{CallID: "c1", Content: "10:00"}}}},
		},
		Tools: []ToolSpec{{Name: "current_time", Description: "time"}},
	}
}

func collect(t *testing.T, p Provider, req Request) (Response, string) {
	t.Helper()
	var streamed strings.Builder
	resp, err := p.Stream(context.Background(), req, func(d Delta) { streamed.WriteString(d.Text) })
	if err != nil {
		t.Fatal(err)
	}
	return resp, streamed.String()
}

func TestOpenAIChat(t *testing.T) {
	f := newFake(t, 200, `data: {"choices":[{"delta":{"reasoning_content":"想"}}]}

data: {"choices":[{"delta":{"content":"你"}}]}

data: {"choices":[{"delta":{"content":"好"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c2","function":{"name":"notify","arguments":"{\"te"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"xt\":1}"}}]}}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"prompt_cache_hit_tokens":8}}

data: [DONE]

`)
	p, _ := New(Config{Name: "ds", Kind: "openai-chat", BaseURL: f.URL + "/", APIKey: "k"})
	resp, streamed := collect(t, p, conversation())

	if f.path != "/chat/completions" || f.header.Get("Authorization") != "Bearer k" {
		t.Fatalf("path %s auth %q", f.path, f.header.Get("Authorization"))
	}
	msgs := f.body["messages"].([]any)
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" || msgs[3].(map[string]any)["role"] != "tool" ||
		msgs[3].(map[string]any)["tool_call_id"] != "c1" {
		t.Fatalf("messages: %v", msgs)
	}
	if calls := msgs[2].(map[string]any)["tool_calls"].([]any); len(calls) != 1 {
		t.Fatalf("assistant tool calls: %v", calls)
	}
	if streamed != "你好" || resp.Message.PlainText() != "你好" {
		t.Fatalf("text %q / %q", streamed, resp.Message.PlainText())
	}
	calls := resp.Message.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "c2" || calls[0].Name != "notify" || string(calls[0].Args) != `{"text":1}` {
		t.Fatalf("calls: %+v", calls)
	}
	if resp.Usage != (Usage{InputTokens: 12, OutputTokens: 3, CachedTokens: 8}) {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

func TestOpenAIResponses(t *testing.T) {
	f := newFake(t, 200, `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"好的"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"c3","name":"notify","arguments":"{\"a\":1}"}}

event: response.completed
data: {"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":4,"input_tokens_details":{"cached_tokens":5}}}}

`)
	p, _ := New(Config{Name: "oa", Kind: "openai-responses", BaseURL: f.URL, APIKey: "k"})
	resp, streamed := collect(t, p, conversation())

	if f.path != "/responses" || f.body["instructions"] != "你是秘书" || f.body["store"] != false {
		t.Fatalf("request: %s %v", f.path, f.body)
	}
	input := f.body["input"].([]any)
	if len(input) != 3 || input[1].(map[string]any)["type"] != "function_call" || input[2].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("input: %v", input)
	}
	if streamed != "好的" || len(resp.Message.ToolCalls()) != 1 || resp.Message.ToolCalls()[0].ID != "c3" {
		t.Fatalf("resp: %+v", resp)
	}
	if resp.Usage != (Usage{InputTokens: 20, OutputTokens: 4, CachedTokens: 5}) {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

func TestOpenAIResponsesFailure(t *testing.T) {
	f := newFake(t, 200, `data: {"type":"response.failed","response":{"error":{"message":"quota"}}}

`)
	p, _ := New(Config{Name: "oa", Kind: "openai-responses", BaseURL: f.URL})
	if _, err := p.Stream(context.Background(), conversation(), nil); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnthropicMessages(t *testing.T) {
	f := newFake(t, 200, `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":3}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"嗯"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu1","name":"notify"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"2}"}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":9}}

event: message_stop
data: {"type":"message_stop"}

`)
	p, _ := New(Config{Name: "an", Kind: "anthropic-messages", BaseURL: f.URL, APIKey: "k"})
	resp, streamed := collect(t, p, conversation())

	if f.path != "/v1/messages" || f.header.Get("x-api-key") != "k" || f.header.Get("anthropic-version") == "" {
		t.Fatalf("request: %s %v", f.path, f.header)
	}
	if f.body["system"] != "你是秘书" || f.body["max_tokens"].(float64) != anthropicDefaultMaxTokens {
		t.Fatalf("body: %v", f.body)
	}
	msgs := f.body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if len(msgs) != 3 || last["role"] != "user" || last["content"].([]any)[0].(map[string]any)["type"] != "tool_result" {
		t.Fatalf("messages: %v", msgs)
	}
	calls := resp.Message.ToolCalls()
	if streamed != "嗯" || len(calls) != 1 || calls[0].ID != "tu1" || string(calls[0].Args) != `{"x":2}` {
		t.Fatalf("resp: %q %+v", streamed, calls)
	}
	if resp.Usage != (Usage{InputTokens: 10, OutputTokens: 9, CachedTokens: 3}) {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

// CacheAt puts cache breakpoints on the system prompt and on the last block
// of the marked messages (at most three), and cache writes count as input.
func TestAnthropicCacheBreakpoints(t *testing.T) {
	f := newFake(t, 200, `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":2,"cache_read_input_tokens":30,"cache_creation_input_tokens":5}}}

event: message_stop
data: {"type":"message_stop"}

`)
	p, _ := New(Config{Name: "an", Kind: "anthropic-messages", BaseURL: f.URL, APIKey: "k"})
	req := conversation()
	req.Messages = append([]Message{Text(RoleUser, "摘要"), Text(RoleAssistant, "好")}, req.Messages...)
	req.CacheAt = []int{0, 1, 2, 4}
	resp, _ := collect(t, p, req)
	sys, ok := f.body["system"].([]any)
	if !ok || sys[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("system: %v", f.body["system"])
	}
	marked := 0
	for _, m := range f.body["messages"].([]any) {
		for _, b := range m.(map[string]any)["content"].([]any) {
			if b.(map[string]any)["cache_control"] != nil {
				marked++
			}
		}
	}
	msgs := f.body["messages"].([]any)
	first := msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if marked != 3 || first["cache_control"] != nil {
		t.Fatalf("want the newest 3 breakpoints, got %d: %v", marked, msgs)
	}
	if resp.Usage.InputTokens != 37 || resp.Usage.CachedTokens != 30 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

func TestAPIErrorAndTransient(t *testing.T) {
	f := newFake(t, 429, "")
	p, _ := New(Config{Name: "ds", Kind: "openai-chat", BaseURL: f.URL})
	_, err := p.Stream(context.Background(), conversation(), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 429 || !Transient(err) {
		t.Fatalf("err = %v", err)
	}
	if got := (&APIError{Status: 503, Body: `{"error":{"message":"Service is too busy.","type":"x"}}`}).Error(); got != "HTTP 503: Service is too busy." {
		t.Fatalf("message: %q", got)
	}
	if got := (&APIError{Status: 400, Body: `{"error":"bad model"}`}).Error(); got != "HTTP 400: bad model" {
		t.Fatalf("string error: %q", got)
	}
	if got := (&APIError{Status: 502, Body: "<html>bad gateway</html>"}).Error(); got != "HTTP 502: <html>bad gateway</html>" {
		t.Fatalf("raw body: %q", got)
	}
	if Transient(&APIError{Status: 400}) {
		t.Fatal("400 must not be transient")
	}
	if !Transient(&APIError{Status: 503}) {
		t.Fatal("503 must be transient")
	}
}

type flaky struct {
	calls   atomic.Int32
	failFor int32
	err     error
	stream  bool
}

func (f *flaky) Name() string { return "flaky" }
func (f *flaky) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	n := f.calls.Add(1)
	if f.stream && onDelta != nil {
		onDelta(Delta{Text: "部分"})
	}
	if n <= f.failFor {
		return Response{}, f.err
	}
	return Response{Message: Text(RoleAssistant, "ok")}, nil
}

func TestRetryTransientOnly(t *testing.T) {
	f := &flaky{failFor: 2, err: &APIError{Status: 502}}
	resp, err := WithRetry(f, 3, time.Millisecond).Stream(context.Background(), Request{}, nil)
	if err != nil || resp.Message.PlainText() != "ok" || f.calls.Load() != 3 {
		t.Fatalf("err %v calls %d", err, f.calls.Load())
	}

	f = &flaky{failFor: 5, err: &APIError{Status: 400}}
	if _, err := WithRetry(f, 3, time.Millisecond).Stream(context.Background(), Request{}, nil); err == nil || f.calls.Load() != 1 {
		t.Fatalf("non-transient retried: calls %d", f.calls.Load())
	}

	f = &flaky{failFor: 5, err: &APIError{Status: 502}, stream: true}
	if _, err := WithRetry(f, 3, time.Millisecond).Stream(context.Background(), Request{}, func(Delta) {}); err == nil || f.calls.Load() != 1 {
		t.Fatalf("retried after streaming: calls %d", f.calls.Load())
	}
}

func TestMockCallsTimeTool(t *testing.T) {
	p, _ := New(Config{Name: "mock", Kind: "mock"})
	req := Request{Messages: []Message{Text(RoleUser, "现在几点")}, Tools: []ToolSpec{{Name: "current_time"}}}
	resp, err := p.Stream(context.Background(), req, nil)
	if err != nil || len(resp.Message.ToolCalls()) != 1 {
		t.Fatalf("expected tool call: %v %+v", err, resp)
	}
	req.Messages = append(req.Messages, resp.Message,
		Message{Role: RoleTool, Parts: []Part{{ToolResult: &ToolResult{CallID: "call_mock_1", Content: "10:00"}}}})
	resp, err = p.Stream(context.Background(), req, nil)
	if err != nil || !strings.Contains(resp.Message.PlainText(), "10:00") {
		t.Fatalf("final: %v %q", err, resp.Message.PlainText())
	}
}

func TestUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "nope"}); err == nil || !strings.Contains(err.Error(), "openai-chat") {
		t.Fatalf("err = %v", err)
	}
}

func TestEffortReachesEachKind(t *testing.T) {
	sse := "data: [DONE]\n\n"
	cases := []struct {
		kind  string
		check func(body map[string]any) bool
	}{
		{"openai-chat", func(b map[string]any) bool { return b["reasoning_effort"] == "xhigh" }},
		{"openai-responses", func(b map[string]any) bool {
			r, _ := b["reasoning"].(map[string]any)
			return r["effort"] == "xhigh"
		}},
		{"anthropic-messages", func(b map[string]any) bool {
			o, _ := b["output_config"].(map[string]any)
			return o["effort"] == "max" && b["thinking"] == nil
		}},
	}
	for _, c := range cases {
		f := newFake(t, 200, sse)
		p, err := New(Config{Name: "x", Kind: c.kind, BaseURL: f.URL, APIKey: "k"})
		if err != nil {
			t.Fatal(err)
		}
		WithEffort(p, "max").Stream(context.Background(), Request{Model: "m", Messages: []Message{Text(RoleUser, "hi")}}, func(Delta) {})
		if !c.check(f.body) {
			t.Errorf("%s: effort not sent: %v", c.kind, f.body)
		}
	}
	// No effort configured: nothing is sent.
	f := newFake(t, 200, sse)
	p, _ := New(Config{Name: "x", Kind: "openai-chat", BaseURL: f.URL})
	WithEffort(p, "").Stream(context.Background(), Request{Model: "m", Messages: []Message{Text(RoleUser, "hi")}}, func(Delta) {})
	if _, ok := f.body["reasoning_effort"]; ok {
		t.Errorf("effort sent without being set: %v", f.body)
	}
}

func TestImagesReachEachKind(t *testing.T) {
	img := &Image{Mime: "image/png", Data: []byte("PNG")}
	req := Request{Model: "m", Messages: []Message{{Role: RoleUser, Parts: []Part{{Text: "看图"}, {Image: img}}}}}
	b64 := "UE5H"

	chat, _ := json.Marshal(ocMessages(req))
	if !strings.Contains(string(chat), `"image_url":{"url":"data:image/png;base64,`+b64+`"}`) || !strings.Contains(string(chat), `"text":"看图"`) {
		t.Errorf("openai-chat: %s", chat)
	}
	resp, _ := json.Marshal(orInput(req))
	if !strings.Contains(string(resp), `{"image_url":"data:image/png;base64,`+b64+`","type":"input_image"}`) {
		t.Errorf("openai-responses: %s", resp)
	}
	an, _ := json.Marshal(anMessages(req))
	if !strings.Contains(string(an), `"source":{"data":"`+b64+`","media_type":"image/png","type":"base64"}`) {
		t.Errorf("anthropic: %s", an)
	}

	// Text-only messages keep the plain string form.
	plain, _ := json.Marshal(ocMessages(Request{Messages: []Message{Text(RoleUser, "hi")}}))
	if string(plain) != `[{"role":"user","content":"hi"}]` {
		t.Errorf("plain chat message: %s", plain)
	}
}

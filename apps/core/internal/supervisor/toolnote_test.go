package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// failing is a tool that always fails with a two-line error.
type failing struct{}

func (failing) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: "fail", Schema: json.RawMessage(`{"type":"object"}`)}
}

func (failing) Call(context.Context, json.RawMessage) (string, error) {
	return "", errors.New("机器离线\n第二行不显示")
}

func respond(text string, calls ...provider.ToolCall) provider.Response {
	m := provider.Message{Role: provider.RoleAssistant}
	if text != "" {
		m.Parts = append(m.Parts, provider.Part{Text: text})
	}
	for i := range calls {
		m.Parts = append(m.Parts, provider.Part{ToolCall: &calls[i]})
	}
	return provider.Response{Message: m}
}

// The supervisor's own tool calls are stored with the message said before
// them (a step), with masked, cut arguments and a one-line error; calls
// made before anything was said go with the next message.
func TestToolCallsAreStoredWithMessages(t *testing.T) {
	long := strings.Repeat("ls -la ", 30)
	p := &scripted{responses: []provider.Response{
		respond("", provider.ToolCall{ID: "a", Name: "count", Args: json.RawMessage(`{"i":1}`)}),
		respond("先看一下机器", provider.ToolCall{ID: "b", Name: "count",
			Args: json.RawMessage(`{"command":"` + long + `","api_key":"plain-value","note":"token ghp_abcdefghijklmnopqrstuvwxyz0123"}`)}),
		respond("", provider.ToolCall{ID: "c", Name: "fail", Args: json.RawMessage(`{}`)}),
		respond("好了"),
	}}
	log, main, _ := setup(t, p, tool.NewRegistry(&counter{}, failing{}))
	say(t, log, main, "查查")
	msgs := waitMessages(t, log, main, 3)
	step, last := msgs[1], msgs[2]
	if step.Kind != event.KindStep || step.Content != "先看一下机器" || last.Content != "好了" {
		t.Fatalf("messages: %+v", msgs)
	}
	// The first call came before any words: it goes with the step.
	if len(step.Tools) != 3 {
		t.Fatalf("step tools: %+v", step.Tools)
	}
	if n := step.Tools[0]; n.Name != "count" || n.Args != "i=1" || n.Error != "" {
		t.Fatalf("first: %+v", n)
	}
	n := step.Tools[1]
	if !strings.Contains(n.Args, "api_key=***") || strings.Contains(n.Args, "plain-value") || strings.Contains(n.Args, "ghp_") {
		t.Fatalf("not masked: %q", n.Args)
	}
	if !strings.Contains(n.Args, "command="+long[:noteValueRunes]+"…") {
		t.Fatalf("command not cut to %d: %q", noteValueRunes, n.Args)
	}
	if n := step.Tools[2]; n.Name != "fail" || n.Error != "机器离线" {
		t.Fatalf("failed call: %+v", n)
	}
	if len(last.Tools) != 0 {
		t.Fatalf("reply tools: %+v", last.Tools)
	}
}

// When nothing was said before the tools, they go with the reply.
func TestToolCallsGoWithTheReply(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		respond("", provider.ToolCall{ID: "a", Name: "count", Args: json.RawMessage(`{}`)}),
		respond("好了"),
	}}
	log, main, _ := setup(t, p, tool.NewRegistry(&counter{}))
	say(t, log, main, "查查")
	msgs := waitMessages(t, log, main, 2)
	if last := msgs[1]; last.Content != "好了" || len(last.Tools) != 1 || last.Tools[0].Name != "count" {
		t.Fatalf("reply: %+v", last)
	}
}

func TestArgsSummary(t *testing.T) {
	cases := map[string]string{
		`{"b":2,"a":"x\ny"}`:            "a=x y b=2",
		`{"password":"hunter2"}`:        "password=***",
		`{"cmd":"export GH_TOKEN=abc"}`: "cmd=export GH_TOKEN=***",
		`not json`:                      "not json",
	}
	for in, want := range cases {
		if got := argsSummary(json.RawMessage(in)); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

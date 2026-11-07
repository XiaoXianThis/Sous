package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// counter is a tool that counts its calls.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: "count", Schema: json.RawMessage(`{"type":"object"}`)}
}

func (c *counter) Call(context.Context, json.RawMessage) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return fmt.Sprintf("第 %d 次", c.n), nil
}

func (c *counter) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// looping calls a tool every step (with new arguments, or the same ones)
// until the results ask it to wrap up; then it reports.
type looping struct {
	mu       sync.Mutex
	same     bool
	stopAt   int // reply with text at this request (0: only when asked to wrap up)
	requests int
}

func (l *looping) Name() string { return "looping" }
func (l *looping) Stream(_ context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests++
	last := req.Messages[len(req.Messages)-1]
	if last.Role == provider.RoleTool {
		r := last.Parts[len(last.Parts)-1].ToolResult
		if strings.Contains(r.Content, "快到上限") {
			return provider.Response{Message: provider.Text(provider.RoleAssistant, "做到第 49 步了，还剩收尾")}, nil
		}
	}
	if l.stopAt > 0 && l.requests == l.stopAt {
		return provider.Response{Message: provider.Text(provider.RoleAssistant, "好了")}, nil
	}
	args := fmt.Sprintf(`{"i":%d}`, l.requests)
	if l.same {
		args = `{}`
	}
	call := &provider.ToolCall{ID: fmt.Sprint("c", l.requests), Name: "count", Args: json.RawMessage(args)}
	return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{{ToolCall: call}}}}, nil
}

// A long turn may use up to MaxSteps model calls; before the last one the
// model is told to stop calling tools and report where it is, and its
// report is the reply (decision 0024).
func TestLongTurnWrapsUp(t *testing.T) {
	if MaxSteps != 50 {
		t.Fatalf("MaxSteps = %d", MaxSteps)
	}
	p := &looping{}
	c := &counter{}
	log, main, _ := setup(t, p, tool.NewRegistry(c))
	say(t, log, main, "一直查下去")
	msgs := waitMessages(t, log, main, 2)
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "做到第 49 步") || strings.Contains(last.Content, "已停止") {
		t.Fatalf("reply: %q", last.Content)
	}
	if p.requests != MaxSteps || c.calls() != MaxSteps-1 {
		t.Fatalf("requests %d, tool calls %d", p.requests, c.calls())
	}
}

// The very same tool call runs at most twice in one turn; the copies after
// that are refused with a hint instead of run.
func TestSameCallIsNotRepeated(t *testing.T) {
	p := &looping{same: true, stopAt: 5}
	c := &counter{}
	log, main, _ := setup(t, p, tool.NewRegistry(c))
	say(t, log, main, "看看")
	msgs := waitMessages(t, log, main, 2)
	if msgs[len(msgs)-1].Content != "好了" || c.calls() != maxSameCall {
		t.Fatalf("reply %q, tool calls %d", msgs[len(msgs)-1].Content, c.calls())
	}
}

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// mock is an offline provider for development and tests. It streams an echo
// of the last user message. When the user asks for the time and a
// current_time tool is offered, it calls that tool first. The model
// "verdict" answers with a fixed JSON verdict instead, so the reviewer
// passes and the approver hands every decision to the user (mock/verdict
// for the reviewer, approver and inspector roles).
type mock struct{ cfg Config }

func init() {
	Register("mock", func(c Config) (Provider, error) { return &mock{cfg: c}, nil })
}

func (p *mock) Name() string { return p.cfg.Name }

func (p *mock) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	var lastUser string
	var lastResult *ToolResult
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			// The first part is what was said; the supervisor appends the
			// turn's environment as a later part.
			lastUser, lastResult = "", nil
			if len(m.Parts) > 0 {
				lastUser = m.Parts[0].Text
			}
		case RoleTool:
			for _, part := range m.Parts {
				if part.ToolResult != nil {
					lastResult = part.ToolResult
				}
			}
		}
	}
	usage := Usage{InputTokens: len([]rune(req.System + lastUser)), OutputTokens: 0}
	if req.Model == "verdict" {
		return Response{Message: Text(RoleAssistant, `{"pass":true,"decision":"escalate","severity":"low","action":"none","reason":"（模拟模型）没有发现问题，交给你决定"}`), Usage: usage}, nil
	}

	if lastResult == nil && strings.Contains(lastUser, "几点") && hasTool(req.Tools, "current_time") {
		return Response{
			Message: Message{Role: RoleAssistant, Parts: []Part{{ToolCall: &ToolCall{ID: "call_mock_1", Name: "current_time", Args: json.RawMessage(`{}`)}}}},
			Usage:   usage,
		}, nil
	}

	reply := fmt.Sprintf("（模拟模型）收到：%s", lastUser)
	if lastResult != nil {
		reply = fmt.Sprintf("（模拟模型）现在是 %s。", lastResult.Content)
	}
	var sent strings.Builder
	for _, r := range reply {
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(15 * time.Millisecond):
		}
		sent.WriteRune(r)
		if onDelta != nil {
			onDelta(Delta{Text: string(r)})
		}
	}
	usage.OutputTokens = len([]rune(reply))
	return Response{Message: Text(RoleAssistant, sent.String()), Usage: usage}, nil
}

func hasTool(tools []ToolSpec, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

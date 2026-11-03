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
// current_time tool is offered, it calls that tool first.
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
			lastUser, lastResult = m.PlainText(), nil
		case RoleTool:
			for _, part := range m.Parts {
				if part.ToolResult != nil {
					lastResult = part.ToolResult
				}
			}
		}
	}
	usage := Usage{InputTokens: len([]rune(req.System + lastUser)), OutputTokens: 0}

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

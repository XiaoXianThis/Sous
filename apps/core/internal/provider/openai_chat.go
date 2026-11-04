package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// openaiChat speaks the OpenAI Chat Completions format, which DeepSeek and
// most OpenAI-compatible services accept. base_url includes the version path
// when the service uses one (https://api.openai.com/v1, https://api.deepseek.com).
type openaiChat struct {
	cfg     Config
	session string // fallback x-opencode-session when a request has none
}

func init() {
	Register("openai-chat", func(c Config) (Provider, error) {
		var b [8]byte
		_, _ = rand.Read(b[:])
		return &openaiChat{cfg: c, session: "sous-" + hex.EncodeToString(b[:])}, nil
	})
}

func (p *openaiChat) Name() string { return p.cfg.Name }

type ocMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []ocToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type ocToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func ocMessages(req Request) []ocMessage {
	str := func(s string) *string { return &s }
	var out []ocMessage
	if req.System != "" {
		out = append(out, ocMessage{Role: "system", Content: str(req.System)})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleTool:
			for _, part := range m.Parts {
				if r := part.ToolResult; r != nil {
					out = append(out, ocMessage{Role: "tool", ToolCallID: r.CallID, Content: str(r.Content)})
				}
			}
		case RoleAssistant:
			msg := ocMessage{Role: "assistant"}
			if t := m.PlainText(); t != "" {
				msg.Content = str(t)
			}
			for _, c := range m.ToolCalls() {
				tc := ocToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(argsOrEmpty(c.Args))
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
			if msg.Content == nil && len(msg.ToolCalls) == 0 {
				msg.Content = str("")
			}
			out = append(out, msg)
		default:
			out = append(out, ocMessage{Role: "user", Content: str(m.PlainText())})
		}
	}
	return out
}

func (p *openaiChat) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       ocMessages(req),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type":     "function",
				"function": map[string]any{"name": t.Name, "description": t.Description, "parameters": emptySchema(t.Schema)},
			})
		}
		body["tools"] = tools
	}
	session := req.SessionID
	if session == "" {
		session = p.session
	}
	resp, err := postStream(ctx, p.cfg.Client, p.cfg.BaseURL+"/chat/completions",
		map[string]string{
			"Authorization":      "Bearer " + p.cfg.APIKey,
			"User-Agent":         "sous-hub/1.0",
			"x-opencode-session": session,
		}, body)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	var text string
	calls := map[int]*ToolCall{}
	var out Response
	err = readSSE(resp.Body, func(ev sseEvent) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string       `json:"content"`
					ReasoningContent string       `json:"reasoning_content"`
					ToolCalls        []ocToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens         int `json:"prompt_tokens"`
				CompletionTokens     int `json:"completion_tokens"`
				PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
				PromptTokensDetails  struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return err
		}
		if u := chunk.Usage; u != nil {
			out.Usage = Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens,
				CachedTokens: max(u.PromptCacheHitTokens, u.PromptTokensDetails.CachedTokens)}
		}
		for _, c := range chunk.Choices {
			d := c.Delta
			if d.ReasoningContent != "" && onDelta != nil {
				onDelta(Delta{Thinking: d.ReasoningContent})
			}
			if d.Content != "" {
				text += d.Content
				if onDelta != nil {
					onDelta(Delta{Text: d.Content})
				}
			}
			for i, tc := range d.ToolCalls {
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				call := calls[idx]
				if call == nil {
					call = &ToolCall{}
					calls[idx] = call
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Name = tc.Function.Name
				}
				call.Args = append(call.Args, tc.Function.Arguments...)
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	out.Message = Message{Role: RoleAssistant}
	if text != "" {
		out.Message.Parts = append(out.Message.Parts, Part{Text: text})
	}
	idxs := make([]int, 0, len(calls))
	for i := range calls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		c := calls[i]
		c.Args = argsOrEmpty(c.Args)
		out.Message.Parts = append(out.Message.Parts, Part{ToolCall: c})
	}
	return out, nil
}

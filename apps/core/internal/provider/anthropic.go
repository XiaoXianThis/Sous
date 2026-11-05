package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// anthropicMessages speaks the Anthropic Messages API. base_url is the host
// without the version path, e.g. https://api.anthropic.com.
type anthropicMessages struct{ cfg Config }

func init() {
	Register("anthropic-messages", func(c Config) (Provider, error) { return &anthropicMessages{cfg: c}, nil })
}

func (p *anthropicMessages) Name() string { return p.cfg.Name }

const anthropicDefaultMaxTokens = 8192

// anMessages converts to Anthropic content blocks. Tool results travel in user
// messages, and consecutive messages of the same role are merged. The last
// block of each message in req.CacheAt gets a cache breakpoint (at most
// three, the newest kept; the system prompt takes the fourth).
func anMessages(req Request) []map[string]any {
	cache := map[int]bool{}
	at := req.CacheAt
	if len(at) > 3 {
		at = at[len(at)-3:]
	}
	for _, i := range at {
		cache[i] = true
	}
	var out []map[string]any
	add := func(role string, blocks []map[string]any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1]["role"] == role {
			out[n-1]["content"] = append(out[n-1]["content"].([]map[string]any), blocks...)
			return
		}
		out = append(out, map[string]any{"role": role, "content": blocks})
	}
	for mi, m := range req.Messages {
		var blocks []map[string]any
		role := "user"
		if m.Role == RoleAssistant {
			role = "assistant"
		}
		for _, part := range m.Parts {
			switch {
			case part.ToolCall != nil:
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": part.ToolCall.ID,
					"name": part.ToolCall.Name, "input": argsOrEmpty(part.ToolCall.Args)})
			case part.ToolResult != nil:
				blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": part.ToolResult.CallID,
					"content": part.ToolResult.Content, "is_error": part.ToolResult.IsError})
			case part.Image != nil:
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": part.Image.Mime, "data": base64.StdEncoding.EncodeToString(part.Image.Data)}})
			case part.Text != "":
				blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
			}
		}
		if cache[mi] && len(blocks) > 0 {
			blocks[len(blocks)-1]["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		add(role, blocks)
	}
	return out
}

func (p *anthropicMessages) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	body := map[string]any{
		"model":      req.Model,
		"messages":   anMessages(req),
		"max_tokens": maxTokens,
		"stream":     true,
	}
	if req.System != "" {
		if req.CacheAt != nil {
			// Caching the system prompt caches the tools before it too.
			body["system"] = []map[string]any{{"type": "text", "text": req.System, "cache_control": map[string]any{"type": "ephemeral"}}}
		} else {
			body["system"] = req.System
		}
	}
	if req.Effort != "" {
		// Thinking stays at the model's default (adaptive on current
		// models); effort sets how deep it goes.
		body["output_config"] = map[string]any{"effort": req.Effort}
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": emptySchema(t.Schema)})
		}
		body["tools"] = tools
	}
	resp, err := postStream(ctx, p.cfg.Client, p.cfg.BaseURL+"/v1/messages",
		map[string]string{"x-api-key": p.cfg.APIKey, "anthropic-version": "2023-06-01"}, body)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	type block struct {
		kind string
		text string
		call *ToolCall
	}
	blocks := map[int]*block{}
	var order []int
	var out Response
	err = readSSE(resp.Body, func(ev sseEvent) error {
		var e struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
			return err
		}
		switch e.Type {
		case "message_start":
			u := e.Message.Usage
			out.Usage.InputTokens = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			out.Usage.CachedTokens = e.Message.Usage.CacheReadInputTokens
		case "content_block_start":
			b := &block{kind: e.ContentBlock.Type}
			if b.kind == "tool_use" {
				b.call = &ToolCall{ID: e.ContentBlock.ID, Name: e.ContentBlock.Name}
			}
			blocks[e.Index] = b
			order = append(order, e.Index)
		case "content_block_delta":
			b := blocks[e.Index]
			if b == nil {
				return nil
			}
			switch e.Delta.Type {
			case "text_delta":
				b.text += e.Delta.Text
				if onDelta != nil {
					onDelta(Delta{Text: e.Delta.Text})
				}
			case "thinking_delta":
				if onDelta != nil {
					onDelta(Delta{Thinking: e.Delta.Thinking})
				}
			case "input_json_delta":
				b.call.Args = append(b.call.Args, e.Delta.PartialJSON...)
			}
		case "message_delta":
			out.Usage.OutputTokens = e.Usage.OutputTokens
		case "error":
			return errors.New(e.Error.Message)
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	out.Message = Message{Role: RoleAssistant}
	for _, i := range order {
		b := blocks[i]
		switch b.kind {
		case "text":
			if b.text != "" {
				out.Message.Parts = append(out.Message.Parts, Part{Text: b.text})
			}
		case "tool_use":
			b.call.Args = argsOrEmpty(b.call.Args)
			out.Message.Parts = append(out.Message.Parts, Part{ToolCall: b.call})
		}
	}
	return out, nil
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
)

// openaiResponses speaks the OpenAI Responses API. base_url includes the
// version path, e.g. https://api.openai.com/v1.
type openaiResponses struct{ cfg Config }

func init() {
	Register("openai-responses", func(c Config) (Provider, error) { return &openaiResponses{cfg: c}, nil })
}

func (p *openaiResponses) Name() string { return p.cfg.Name }

func orInput(req Request) []map[string]any {
	var items []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case RoleTool:
			for _, part := range m.Parts {
				if r := part.ToolResult; r != nil {
					items = append(items, map[string]any{"type": "function_call_output", "call_id": r.CallID, "output": r.Content})
				}
			}
		case RoleAssistant:
			if t := m.PlainText(); t != "" {
				items = append(items, map[string]any{"role": "assistant",
					"content": []map[string]any{{"type": "output_text", "text": t}}})
			}
			for _, c := range m.ToolCalls() {
				items = append(items, map[string]any{"type": "function_call", "call_id": c.ID, "name": c.Name,
					"arguments": string(argsOrEmpty(c.Args))})
			}
		default:
			content := []map[string]any{{"type": "input_text", "text": m.PlainText()}}
			if m.hasImage() {
				content = nil
				for _, p := range m.Parts {
					switch {
					case p.Image != nil:
						content = append(content, map[string]any{"type": "input_image", "image_url": p.Image.dataURL()})
					case p.Text != "":
						content = append(content, map[string]any{"type": "input_text", "text": p.Text})
					}
				}
			}
			items = append(items, map[string]any{"role": "user", "content": content})
		}
	}
	return items
}

func (p *openaiResponses) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	body := map[string]any{
		"model":  req.Model,
		"input":  orInput(req),
		"stream": true,
		"store":  false,
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.Effort != "" {
		body["reasoning"] = map[string]any{"effort": openaiEffort(req.Effort)}
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": t.Name,
				"description": t.Description, "parameters": emptySchema(t.Schema)})
		}
		body["tools"] = tools
	}
	resp, err := postStream(ctx, p.cfg.Client, p.cfg.BaseURL+"/responses",
		map[string]string{"Authorization": "Bearer " + p.cfg.APIKey}, body)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	var text string
	var calls []ToolCall
	var out Response
	err = readSSE(resp.Body, func(ev sseEvent) error {
		var e struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Item  struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response struct {
				Usage struct {
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					InputTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &e); err != nil {
			return err
		}
		switch e.Type {
		case "response.output_text.delta":
			text += e.Delta
			if onDelta != nil {
				onDelta(Delta{Text: e.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if onDelta != nil {
				onDelta(Delta{Thinking: e.Delta})
			}
		case "response.output_item.done":
			if e.Item.Type == "function_call" {
				calls = append(calls, ToolCall{ID: e.Item.CallID, Name: e.Item.Name, Args: argsOrEmpty(json.RawMessage(e.Item.Arguments))})
			}
		case "response.completed", "response.incomplete":
			u := e.Response.Usage
			out.Usage = Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.InputTokensDetails.CachedTokens}
		case "response.failed":
			msg := "response failed"
			if e.Response.Error != nil {
				msg = e.Response.Error.Message
			}
			return errors.New(msg)
		case "error":
			return errors.New(e.Message)
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
	for i := range calls {
		out.Message.Parts = append(out.Message.Parts, Part{ToolCall: &calls[i]})
	}
	return out, nil
}

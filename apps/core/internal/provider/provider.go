// Package provider talks to language models. Each wire format (OpenAI Chat
// Completions, OpenAI Responses, Anthropic Messages) is an adapter between the
// unified Message format below and that API.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // carries tool results
)

// Part is one piece of a message: text, a tool call or a tool result.
type Part struct {
	Text       string      `json:"text,omitempty"`
	ToolCall   *ToolCall   `json:"tool_call,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Text returns a message with a single text part.
func Text(role Role, s string) Message { return Message{Role: role, Parts: []Part{{Text: s}}} }

// PlainText joins the text parts of m.
func (m Message) PlainText() string {
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

// ToolCalls returns the tool calls in m.
func (m Message) ToolCalls() []ToolCall {
	var out []ToolCall
	for _, p := range m.Parts {
		if p.ToolCall != nil {
			out = append(out, *p.ToolCall)
		}
	}
	return out
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"` // JSON Schema of the arguments
}

type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
	// SessionID is a stable id for one conversation. Gateways such as
	// OpenCode Go use it for routing and prompt caching. Optional.
	SessionID string
}

// Delta is streamed while a response is generated.
type Delta struct {
	Text     string
	Thinking string
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

// Response is the complete assistant turn.
type Response struct {
	Message Message
	Usage   Usage
}

// Provider streams one model response. onDelta may be nil.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error)
}

// Config configures one provider instance.
type Config struct {
	Name    string
	Kind    string
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// Factory creates a provider from its config.
type Factory func(Config) (Provider, error)

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a provider kind available. It is called from init().
func Register(kind string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	factories[kind] = f
}

// Kinds lists the registered provider kinds.
func Kinds() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// New creates a provider of cfg.Kind.
func New(cfg Config) (Provider, error) {
	regMu.RLock()
	f, ok := factories[cfg.Kind]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider kind %q (known: %s)", cfg.Kind, strings.Join(Kinds(), ", "))
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return f(cfg)
}

// APIError is a non-2xx answer from a provider.
type APIError struct {
	Status int
	Body   string
}

// Error shows the provider's own message when the body is a JSON error
// ({"error": {"message": ...}} or {"error": "..."}), else the raw body.
func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Body)
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &body) == nil && len(body.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		var str string
		if json.Unmarshal(body.Error, &obj) == nil && obj.Message != "" {
			msg = obj.Message
		} else if json.Unmarshal(body.Error, &str) == nil && str != "" {
			msg = str
		}
	}
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, msg)
}

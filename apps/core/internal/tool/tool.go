// Package tool holds the tools the supervisor can call. Built-in tools register
// themselves into a Registry; remote MCP tools will join the same registry.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
)

// Tool is one callable capability.
type Tool interface {
	Spec() provider.ToolSpec
	// Call runs the tool. A returned error is reported to the model as a
	// failed result, not as a turn failure.
	Call(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Spec().Name] = t
}

// Specs returns the tool specs sorted by name, so prompts stay stable.
func (r *Registry) Specs() []provider.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]provider.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Spec())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Call runs a tool call and returns its result for the model.
func (r *Registry) Call(ctx context.Context, c provider.ToolCall) provider.ToolResult {
	r.mu.RLock()
	t, ok := r.tools[c.Name]
	r.mu.RUnlock()
	if !ok {
		return provider.ToolResult{CallID: c.ID, Content: fmt.Sprintf("unknown tool %q", c.Name), IsError: true}
	}
	out, err := safeCall(ctx, t, c.Args)
	if err != nil {
		return provider.ToolResult{CallID: c.ID, Content: err.Error(), IsError: true}
	}
	return provider.ToolResult{CallID: c.ID, Content: out}
}

func safeCall(ctx context.Context, t Tool, args json.RawMessage) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tool panicked: %v", p)
		}
	}()
	return t.Call(ctx, args)
}

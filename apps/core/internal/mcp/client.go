// Package mcp lets the supervisor use MCP (Model Context Protocol) servers:
// a small client for the two transports in use (stdio and streamable
// HTTP), a Manager that keeps the configured servers connected and
// registers their tools next to the built-in ones, the HTTP routes the
// page uses, and the tools the agent uses to propose a server itself.
// See docs/decisions/0021-mcp-servers.md.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// protocolVersion is the version we ask for; older ones the server may
// answer with are accepted as well.
const protocolVersion = "2025-06-18"

var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Config is how to reach one server.
type Config struct {
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

// ToolInfo is one tool a server offers.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations struct {
		Title        string `json:"title"`
		ReadOnlyHint bool   `json:"readOnlyHint"`
	} `json:"annotations"`
}

// Content is one piece of a tool result.
type Content struct {
	Type     string `json:"type"` // text | image | audio | resource | resource_link
	Text     string `json:"text"`
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
	URI      string `json:"uri"`
	Resource *struct {
		URI      string `json:"uri"`
		MIMEType string `json:"mimeType"`
		Text     string `json:"text"`
		Blob     string `json:"blob"`
	} `json:"resource"`
}

// CallResult is what tools/call returns.
type CallResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

// RPCError is a JSON-RPC error answered by the server.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// transport moves JSON-RPC messages. Server-initiated requests are
// answered by the transport itself (ping) or refused.
type transport interface {
	request(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
	close()
	// done is closed when the connection is gone.
	done() <-chan struct{}
	// diagnostics returns what helps explain a dead connection (stderr).
	diagnostics() string
	// setProtocol tells the transport the negotiated version (an HTTP header).
	setProtocol(v string)
}

// Client is one live connection to a server.
type Client struct {
	t          transport
	ServerName string
	Version    string
	Protocol   string
	closeOnce  sync.Once
}

// Connect opens the transport and runs the initialize handshake.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	var t transport
	var err error
	switch cfg.Transport {
	case TransportStdio:
		t, err = newStdio(cfg)
	case TransportHTTP:
		t, err = newHTTP(cfg)
	default:
		err = fmt.Errorf("unknown transport %q", cfg.Transport)
	}
	if err != nil {
		return nil, err
	}
	c := &Client{t: t}
	raw, err := t.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "sous", "version": "1"},
	})
	if err != nil {
		d := t.diagnostics()
		t.close()
		if d != "" {
			return nil, fmt.Errorf("%w\n%s", err, d)
		}
		return nil, err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		t.close()
		return nil, fmt.Errorf("bad initialize answer: %w", err)
	}
	ok := false
	for _, v := range supportedVersions {
		ok = ok || v == init.ProtocolVersion
	}
	if !ok {
		t.close()
		return nil, fmt.Errorf("the server speaks MCP version %q, which sous does not support", init.ProtocolVersion)
	}
	c.ServerName, c.Version, c.Protocol = init.ServerInfo.Name, init.ServerInfo.Version, init.ProtocolVersion
	t.setProtocol(init.ProtocolVersion)
	if err := t.notify(ctx, "notifications/initialized", nil); err != nil {
		t.close()
		return nil, err
	}
	return c, nil
}

// ListTools returns every tool, following pagination.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	cursor := ""
	for range 20 {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.t.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []ToolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("bad tools/list answer: %w", err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// CallTool runs one tool. args is a JSON object (or empty).
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	var a any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		a = args
	}
	raw, err := c.t.request(ctx, "tools/call", map[string]any{"name": name, "arguments": a})
	if err != nil {
		return CallResult{}, err
	}
	var r CallResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return CallResult{}, fmt.Errorf("bad tools/call answer: %w", err)
	}
	return r, nil
}

// Done is closed when the connection is gone.
func (c *Client) Done() <-chan struct{} { return c.t.done() }

// Diagnostics is what the server printed to stderr, if anything.
func (c *Client) Diagnostics() string { return c.t.diagnostics() }

func (c *Client) Close() { c.closeOnce.Do(c.t.close) }

// ErrClosed means the connection is gone.
var ErrClosed = errors.New("MCP connection closed")

// idGen hands out JSON-RPC ids.
type idGen struct{ n atomic.Int64 }

func (g *idGen) next() int64 { return g.n.Add(1) }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcMessage is anything that can arrive from the server.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func (m rpcMessage) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// idOf returns the numeric id of a response; ok is false for string ids.
func (m rpcMessage) idOf() (int64, bool) {
	var n int64
	if err := json.Unmarshal(m.ID, &n); err != nil {
		return 0, false
	}
	return n, true
}

// refuse is the answer to server requests we do not implement.
func refuse(id json.RawMessage, method string) []byte {
	if method == "ping" {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": map[string]any{}})
		return b
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id),
		"error": map[string]any{"code": -32601, "message": "method not supported by this client"}})
	return b
}

// maxMessage caps one message from a server.
const maxMessage = 16 << 20

const defaultTimeout = 60 * time.Second

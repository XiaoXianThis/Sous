package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ProtocolVersion is the ACP version sous speaks.
const ProtocolVersion = 1

// Command starts an agent's ACP server.
type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string // added to the current environment
}

// Update is a normalized session/update notification.
type Update struct {
	// Kind is the ACP sessionUpdate value: agent_message_chunk,
	// agent_thought_chunk, tool_call, tool_call_update, plan, and others.
	Kind       string
	Text       string // message and thought chunks
	ToolCallID string
	Title      string
	Status     string // tool call status: pending, in_progress, completed, failed
	ToolKind   string // read, edit, delete, move, search, execute, think, fetch, other
	Plan       []PlanEntry
	Raw        json.RawMessage
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

// PermissionOption is one choice the agent offers.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
}

// PermissionRequest is session/request_permission.
type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallInfo       `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type ToolCallInfo struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// Decision answers a permission request: the chosen option, or none to cancel.
type Decision struct {
	OptionID string
}

// Callbacks receive what the agent sends. Both must be safe to call from
// several goroutines.
type Callbacks struct {
	OnUpdate     func(sessionID string, u Update)
	OnPermission func(ctx context.Context, req PermissionRequest) Decision
}

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type InitializeResult struct {
	ProtocolVersion   int             `json:"protocolVersion"`
	AgentCapabilities json.RawMessage `json:"agentCapabilities"`
	AuthMethods       []AuthMethod    `json:"authMethods"`
	AgentInfo         *struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"agentInfo,omitempty"`
}

// LoadSession reports whether the agent can resume sessions.
func (r InitializeResult) LoadSession() bool {
	var caps struct {
		LoadSession bool `json:"loadSession"`
	}
	json.Unmarshal(r.AgentCapabilities, &caps)
	return caps.LoadSession
}

type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Session struct {
	ID          string
	CurrentMode string
	Modes       []Mode
}

// Client is one running agent process.
type Client struct {
	cmd  *exec.Cmd
	conn *Conn
	cb   Callbacks
	Init InitializeResult

	stderrMu sync.Mutex
	stderr   []byte

	waitOnce sync.Once
	waitErr  error
	exited   chan struct{}
}

// stderrKeep bounds the agent's stderr kept for error messages.
const stderrKeep = 8 * 1024

// Start launches the agent and runs initialize.
func Start(ctx context.Context, c Command, cb Callbacks) (*Client, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	configureProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", c.Path, err)
	}
	cl := &Client{cmd: cmd, cb: cb, exited: make(chan struct{})}
	go cl.collectStderr(stderr)
	cl.conn = NewConn(stdout, stdin, cl.handle)
	go func() {
		<-cl.conn.Done()
		cl.wait()
	}()

	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err = cl.conn.Call(initCtx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]string{"name": "sous", "title": "sous"},
	}, &cl.Init)
	if err != nil {
		cl.Close()
		return nil, cl.withStderr(fmt.Errorf("initialize: %w", err))
	}
	return cl, nil
}

func (c *Client) collectStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderrMu.Lock()
			c.stderr = append(c.stderr, buf[:n]...)
			if len(c.stderr) > stderrKeep {
				c.stderr = c.stderr[len(c.stderr)-stderrKeep:]
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Stderr returns the tail of the agent's stderr.
func (c *Client) Stderr() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.TrimSpace(string(c.stderr))
}

func (c *Client) withStderr(err error) error {
	if s := c.Stderr(); s != "" {
		lines := strings.Split(s, "\n")
		if len(lines) > 5 {
			lines = lines[len(lines)-5:]
		}
		return fmt.Errorf("%w; stderr: %s", err, strings.Join(lines, " | "))
	}
	return err
}

func (c *Client) wait() {
	c.waitOnce.Do(func() {
		c.waitErr = c.cmd.Wait()
		close(c.exited)
	})
}

// Exited is closed when the agent process has ended.
func (c *Client) Exited() <-chan struct{} { return c.exited }

func (c *Client) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "session/update":
		var p struct {
			SessionID string          `json:"sessionId"`
			Update    json.RawMessage `json:"update"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if c.cb.OnUpdate != nil {
			c.cb.OnUpdate(p.SessionID, parseUpdate(p.Update))
		}
		return nil, nil
	case "session/request_permission":
		var req PermissionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		d := Decision{}
		if c.cb.OnPermission != nil {
			d = c.cb.OnPermission(ctx, req)
		}
		if d.OptionID == "" {
			return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
		}
		return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": d.OptionID}}, nil
	}
	// fs/* and terminal/* are not offered in our capabilities.
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not supported by sous: " + method}
}

func parseUpdate(raw json.RawMessage) Update {
	var u struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       json.RawMessage
		ToolCallID    string      `json:"toolCallId"`
		Title         string      `json:"title"`
		Status        string      `json:"status"`
		Kind          string      `json:"kind"`
		Entries       []PlanEntry `json:"entries"`
	}
	json.Unmarshal(raw, &u)
	out := Update{Kind: u.SessionUpdate, ToolCallID: u.ToolCallID, Title: u.Title, Status: u.Status, ToolKind: u.Kind, Plan: u.Entries, Raw: raw}
	// Message chunks carry one content block; tool calls carry a list.
	var block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(u.Content, &block) == nil && block.Type == "text" {
		out.Text = block.Text
	}
	return out
}

// NewSession opens a session in cwd. If the agent asks for authentication,
// it authenticates with the agent's first method and tries once more.
func (c *Client) NewSession(ctx context.Context, cwd string) (Session, error) {
	var res struct {
		SessionID string `json:"sessionId"`
		Modes     *struct {
			CurrentModeID  string `json:"currentModeId"`
			AvailableModes []Mode `json:"availableModes"`
		} `json:"modes"`
	}
	params := map[string]any{"cwd": cwd, "mcpServers": []any{}}
	err := c.conn.Call(ctx, "session/new", params, &res)
	if IsAuthRequired(err) && len(c.Init.AuthMethods) > 0 {
		authCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		aerr := c.conn.Call(authCtx, "authenticate", map[string]string{"methodId": c.Init.AuthMethods[0].ID}, nil)
		cancel()
		if aerr != nil {
			return Session{}, fmt.Errorf("%w: %s (%v)", ErrNotLoggedIn, c.Init.AuthMethods[0].Description, aerr)
		}
		err = c.conn.Call(ctx, "session/new", params, &res)
	}
	if IsAuthRequired(err) {
		return Session{}, fmt.Errorf("%w: %v", ErrNotLoggedIn, err)
	}
	if err != nil {
		return Session{}, c.withStderr(fmt.Errorf("session/new: %w", err))
	}
	s := Session{ID: res.SessionID}
	if res.Modes != nil {
		s.CurrentMode = res.Modes.CurrentModeID
		s.Modes = res.Modes.AvailableModes
	}
	return s, nil
}

// ErrNotLoggedIn means the agent needs its user to log in first.
var ErrNotLoggedIn = errors.New("agent is not logged in")

// SetMode switches the session mode (such as "default", which asks before
// every change).
func (c *Client) SetMode(ctx context.Context, sessionID, modeID string) error {
	return c.conn.Call(ctx, "session/set_mode", map[string]string{"sessionId": sessionID, "modeId": modeID}, nil)
}

// Prompt sends one user turn and waits until the agent ends it. It returns
// the stop reason: end_turn, max_tokens, max_turn_requests, refusal, cancelled.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	var res struct {
		StopReason string `json:"stopReason"`
	}
	err := c.conn.Call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": text}},
	}, &res)
	if IsAuthRequired(err) {
		return "", fmt.Errorf("%w: %v", ErrNotLoggedIn, err)
	}
	if err != nil {
		return "", c.withStderr(err)
	}
	return res.StopReason, nil
}

// Cancel asks the agent to stop the current turn; Prompt then returns
// with stop reason "cancelled".
func (c *Client) Cancel(sessionID string) error {
	return c.conn.Notify("session/cancel", map[string]string{"sessionId": sessionID})
}

// Close ends the agent process and its children.
func (c *Client) Close() error {
	killProcess(c.cmd)
	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
	}
	return nil
}

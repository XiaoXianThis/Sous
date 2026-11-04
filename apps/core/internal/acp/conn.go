// Package acp is a client for the Agent Client Protocol: JSON-RPC 2.0 over an
// agent process's stdin and stdout, one message per line.
//
// sous is the client. It starts a session, sends prompts, receives
// session/update notifications, and answers the agent's permission requests.
// It declares no file-system or terminal capabilities, so agents use their own
// tools inside the working directory.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Error is a JSON-RPC error.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("acp error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("acp error %d: %s", e.Code, e.Message)
}

// Error codes.
const (
	CodeMethodNotFound = -32601
	CodeAuthRequired   = -32000
)

// IsAuthRequired reports whether err asks the client to authenticate.
func IsAuthRequired(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeAuthRequired
}

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
}

// Handler answers requests and notifications from the agent. For a
// notification the result is ignored.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Conn is one JSON-RPC connection.
type Conn struct {
	w       io.Writer
	handler Handler

	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan message
	closed  chan struct{}
	err     error
}

// NewConn starts reading from r. Incoming requests are answered by handler,
// each in its own goroutine.
func NewConn(r io.Reader, w io.Writer, handler Handler) *Conn {
	c := &Conn{w: w, handler: handler, pending: map[int64]chan message{}, closed: make(chan struct{})}
	go c.read(r)
	return c
}

// Done is closed when the connection ends; Err then says why.
func (c *Conn) Done() <-chan struct{} { return c.closed }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // agents sometimes log to stdout; skip non-JSON lines
		}
		switch {
		case m.Method != "" && m.ID != nil:
			go c.answer(m)
		case m.Method != "":
			// Notifications are handled in order, on this goroutine: message
			// chunks must not be reordered, and the updates an agent replays
			// before answering session/load must be seen before the answer.
			// Notification handlers must not block.
			c.handler(context.Background(), m.Method, m.Params)
		case m.ID != nil:
			var id int64
			if json.Unmarshal(*m.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.closed)
}

func (c *Conn) answer(m message) {
	result, err := c.handler(context.Background(), m.Method, m.Params)
	reply := message{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		var rpcErr *Error
		if !errors.As(err, &rpcErr) {
			rpcErr = &Error{Code: -32603, Message: err.Error()}
		}
		reply.Error = rpcErr
	} else {
		raw, merr := json.Marshal(result)
		if merr != nil {
			reply.Error = &Error{Code: -32603, Message: merr.Error()}
		} else {
			reply.Result = raw
		}
	}
	c.write(reply)
}

func (c *Conn) write(m message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(raw, '\n'))
	return err
}

// Call sends a request and waits for its result, decoded into out (may be nil).
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return fmt.Errorf("acp: connection closed: %w", c.err)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw := json.RawMessage(fmt.Sprint(id))
	if err := c.write(message{JSONRPC: "2.0", ID: &idRaw, Method: method, Params: raw}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return fmt.Errorf("acp: connection closed during %s: %w", method, c.Err())
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

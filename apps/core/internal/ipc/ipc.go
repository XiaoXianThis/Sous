// Package ipc is the daemon's local interface (docs/api/core-daemon-ipc.md):
// line-delimited JSON-RPC 2.0 over a Unix socket in the sous home directory
// (Windows 10 and later support Unix sockets too). Access control is the
// file system's: the socket and its directory belong to the user.
package ipc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// maxSocketPath is below the shortest sun_path limit (104 bytes on macOS).
const maxSocketPath = 100

// Path is where the daemon of sous home root listens: root/daemon.sock, or
// a file in the temporary directory named after root when that path is too
// long for a Unix socket.
func Path(root string) string {
	p := filepath.Join(root, "daemon.sock")
	if len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(os.TempDir(), "sous-"+hex.EncodeToString(sum[:6])+".sock")
}

// ErrNotRunning means no daemon answers on the socket.
var ErrNotRunning = errors.New("sous daemon 没有在运行：先运行 `sous daemon`，或者 `sous service start`")

// Listen opens the socket. A stale socket file from a daemon that died is
// removed; a live one means another daemon already runs.
func Listen(path string) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return nil, fmt.Errorf("另一个 sous daemon 已经在运行（%s）", path)
	}
	os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		os.Chmod(path, 0o600)
	}
	return l, nil
}

// Message is one JSON-RPC message.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Handler answers one call. notify sends a notification to the caller
// (events.subscribe streams with it); it is safe to call until ctx ends.
type Handler func(ctx context.Context, method string, params json.RawMessage, notify func(method string, params any) error) (any, error)

// Serve answers connections until ctx ends.
func Serve(ctx context.Context, l net.Listener, h Handler) {
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go serveConn(ctx, c, h)
	}
}

func serveConn(ctx context.Context, c net.Conn, h Handler) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.Close()
	var wmu sync.Mutex
	write := func(m Message) error {
		m.JSONRPC = "2.0"
		raw, _ := json.Marshal(m)
		wmu.Lock()
		defer wmu.Unlock()
		c.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := c.Write(append(raw, '\n'))
		return err
	}
	notify := func(method string, params any) error {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		return write(Message{Method: method, Params: raw})
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Method == "" {
			continue
		}
		go func() {
			res, err := h(ctx, m.Method, m.Params, notify)
			if m.ID == nil {
				return
			}
			out := Message{ID: m.ID}
			if err != nil {
				out.Error = &Error{Code: -32000, Message: err.Error()}
			} else {
				out.Result, _ = json.Marshal(res)
			}
			write(out)
		}()
	}
}

// Client is a connection to the daemon.
type Client struct {
	c      net.Conn
	nextID atomic.Int64
	mu     sync.Mutex
	wait   map[int64]chan Message
	notes  chan Message
	done   chan struct{}
	err    error
}

// Dial connects to the daemon listening at path.
func Dial(path string) (*Client, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, ErrNotRunning
	}
	cl := &Client{c: c, wait: map[int64]chan Message{}, notes: make(chan Message, 256), done: make(chan struct{})}
	go cl.read()
	return cl, nil
}

func (cl *Client) read() {
	defer close(cl.done)
	defer close(cl.notes)
	sc := bufio.NewScanner(cl.c)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.ID != nil && m.Method == "" {
			cl.mu.Lock()
			ch := cl.wait[*m.ID]
			delete(cl.wait, *m.ID)
			cl.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method != "" {
			cl.notes <- m
		}
	}
	cl.err = sc.Err()
	if cl.err == nil {
		cl.err = errors.New("daemon 断开了连接")
	}
}

// Call sends a request and decodes its result into out (may be nil).
func (cl *Client) Call(ctx context.Context, method string, params, out any) error {
	id := cl.nextID.Add(1)
	ch := make(chan Message, 1)
	cl.mu.Lock()
	cl.wait[id] = ch
	cl.mu.Unlock()
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	line, _ := json.Marshal(Message{JSONRPC: "2.0", ID: &id, Method: method, Params: raw})
	if _, err := cl.c.Write(append(line, '\n')); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-cl.done:
		return cl.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notifications delivers what the daemon pushes (such as "event").
func (cl *Client) Notifications() <-chan Message { return cl.notes }

func (cl *Client) Close() error { return cl.c.Close() }

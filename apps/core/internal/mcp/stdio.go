package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// stdio runs the server as a child process and talks newline-delimited
// JSON-RPC over its stdin and stdout.
type stdio struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	ids   idGen
	wmu   sync.Mutex // one writer at a time
	mu    sync.Mutex
	wait  map[int64]chan rpcMessage
	gone  chan struct{}
	err   error
	stder *tail
}

// childEnv is what a server process sees: not the Hub's environment, which
// holds model keys, but the little a program needs to start, plus the
// variables the user set for this server.
func childEnv(extra map[string]string) []string {
	keep := []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TMPDIR", "TZ"}
	if runtime.GOOS == "windows" {
		keep = append(keep, "SystemRoot", "SYSTEMROOT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "PATHEXT", "TEMP", "TMP", "ComSpec")
	}
	var env []string
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func newStdio(cfg Config) (*stdio, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("no command")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = childEnv(cfg.Env)
	cmd.WaitDelay = 2 * time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// exec copies the child's stdout into this pipe and Wait waits for the
	// copy, so the last lines before an exit are not lost.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	s := &stdio{cmd: cmd, in: in, wait: map[int64]chan rpcMessage{}, gone: make(chan struct{}), stder: &tail{max: 4096}}
	cmd.Stderr = s.stder
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, fmt.Errorf("cannot start %q: %w", cfg.Command, err)
	}
	go s.read(pr)
	go func() {
		err := cmd.Wait()
		pw.Close()
		s.fail(fmt.Errorf("the server process exited (%v)", err))
	}()
	return s, nil
}

// fail marks the connection dead and wakes everyone waiting.
func (s *stdio) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	s.err = err
	close(s.gone)
}

func (s *stdio) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			var m rpcMessage
			if json.Unmarshal(line, &m) == nil {
				s.dispatch(m)
			}
		}
		if err != nil {
			s.fail(fmt.Errorf("the server closed its output (%v)", err))
			return
		}
	}
}

// readLine reads one line of at most maxMessage bytes.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, isPrefix, err := br.ReadLine()
		buf = append(buf, part...)
		if len(buf) > maxMessage {
			return nil, errors.New("message too large")
		}
		if err != nil || !isPrefix {
			return buf, err
		}
	}
}

func (s *stdio) dispatch(m rpcMessage) {
	if m.isResponse() {
		id, ok := m.idOf()
		if !ok {
			return
		}
		s.mu.Lock()
		ch := s.wait[id]
		delete(s.wait, id)
		s.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}
	if m.Method != "" && len(m.ID) > 0 { // a request from the server
		s.write(refuse(m.ID, m.Method))
	}
}

func (s *stdio) write(b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.in.Write(append(b, '\n'))
	return err
}

func (s *stdio) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.ids.next()
	ch := make(chan rpcMessage, 1)
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.wait[id] = ch
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.wait, id)
		s.mu.Unlock()
	}
	b, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params})
	if err := s.write(b); err != nil {
		forget()
		return nil, fmt.Errorf("%w: %v", ErrClosed, err)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		forget()
		// Tell the server to stop; best effort.
		n, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}})
		s.write(n)
		return nil, ctx.Err()
	case <-s.gone:
		forget()
		return nil, s.err
	}
}

func (s *stdio) notify(_ context.Context, method string, params any) error {
	b, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	return s.write(b)
}

func (s *stdio) close() {
	s.in.Close()
	s.fail(ErrClosed)
	// Give the process a moment to leave on its own, then stop it.
	time.AfterFunc(time.Second, func() {
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
	})
}

func (s *stdio) done() <-chan struct{} { return s.gone }
func (s *stdio) diagnostics() string   { return s.stder.String() }
func (s *stdio) setProtocol(string)    {}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

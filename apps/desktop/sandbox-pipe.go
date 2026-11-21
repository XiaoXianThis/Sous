package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

type sandboxPipeRequest struct {
	ID       uint64 `json:"id"`
	Action   string `json:"action"`
	Dispatch string `json:"dispatch_id,omitempty"`
	Lease    string `json:"lease,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Changes  int    `json:"changes,omitempty"`
}

type sandboxPipeReply struct {
	ID    uint64          `json:"id"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

type sandboxPipe struct {
	in      io.WriteCloser
	done    chan struct{}
	mu      sync.Mutex
	next    uint64
	waiting map[uint64]chan sandboxPipeReply
}

func newSandboxPipe(w io.WriteCloser) *sandboxPipe {
	return &sandboxPipe{in: w, done: make(chan struct{}), waiting: map[uint64]chan sandboxPipeReply{}}
}

func (p *sandboxPipe) pump(scan *bufio.Scanner) {
	defer close(p.done)
	for scan.Scan() {
		var r sandboxPipeReply
		if json.Unmarshal(scan.Bytes(), &r) != nil || r.ID == 0 {
			_ = p.in.Close()
			return
		}
		p.mu.Lock()
		c := p.waiting[r.ID]
		delete(p.waiting, r.ID)
		p.mu.Unlock()
		if c != nil {
			c <- r
		}
	}
	_ = p.in.Close()
}

var errNoSandboxPipe = errors.New("请先在托盘打开由此桌面程序启动的本机工作区")

func (p *sandboxPipe) call(ctx context.Context, r sandboxPipeRequest, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return errNoSandboxPipe
	default:
	}
	p.mu.Lock()
	p.next++
	r.ID = p.next
	// Atomic small line writes to stdin; a timed-out write closes the pipe
	// so a potentially half-written action can never be interpreted later.
	ch := make(chan sandboxPipeReply, 1)
	p.waiting[r.ID] = ch
	if dst, ok := p.in.(interface{ SetWriteDeadline(time.Time) error }); ok {
		at, _ := ctx.Deadline()
		_ = dst.SetWriteDeadline(at)
	}
	err := json.NewEncoder(p.in).Encode(r)
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.waiting, r.ID); p.mu.Unlock() }()
	if err != nil {
		_ = p.in.Close()
		return errNoSandboxPipe
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errNoSandboxPipe
	case reply := <-ch:
		if reply.Error != "" {
			return errors.New(reply.Error)
		}
		return json.Unmarshal(reply.Data, out)
	}
}

func (h *LocalHub) dispatch(ctx context.Context, r sandboxPipeRequest, out any) error {
	h.mu.Lock()
	p := h.pipe
	h.mu.Unlock()
	if p == nil {
		return errNoSandboxPipe
	}
	return p.call(ctx, r, out)
}

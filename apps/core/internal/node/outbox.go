package node

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// maxEphemeral bounds queued frames that may be dropped (agent text), so a
// slow connection cannot grow the queue without limit.
const maxEphemeral = 2000

// compactAfter is how many acknowledgements the outbox file collects
// before it is rewritten with only the pending frames.
const compactAfter = 500

// outbox orders frames to the Hub. Reliable frames get a seq and stay
// pending until the Hub acknowledges them; after a reconnect every pending
// frame is sent again, in order. Ephemeral frames are sent only while
// connected and are never resent.
//
// With a file, pending frames also survive a restart of the daemon: each
// reliable frame and each acknowledgement is appended to the file, which
// is replayed on start and rewritten once enough acknowledgements pile up.
type outbox struct {
	mu        sync.Mutex
	cond      *sync.Cond
	seq       int64
	pending   []proto.Frame // sent or waiting, not yet acknowledged
	queue     []proto.Frame // to write on the current connection
	connected bool
	epoch     int // bumped on every connect and disconnect

	path  string
	file  *os.File
	acked int // acknowledgements written since the last compaction
}

// line is one record of the outbox file.
type line struct {
	F *proto.Frame `json:"f,omitempty"` // a reliable frame
	A int64        `json:"a,omitempty"` // frames up to this seq were acknowledged
	S int64        `json:"s,omitempty"` // the seq counter, written on compaction
}

func newOutbox() *outbox {
	o := &outbox{}
	o.cond = sync.NewCond(&o.mu)
	return o
}

// openOutbox loads the outbox kept in path, creating it if needed.
func openOutbox(path string) (*outbox, error) {
	o := newOutbox()
	o.path = path
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16<<20)
		for sc.Scan() {
			var l line
			if json.Unmarshal(sc.Bytes(), &l) != nil {
				continue // a torn last line after a crash
			}
			switch {
			case l.F != nil:
				o.pending = append(o.pending, *l.F)
				o.seq = max(o.seq, l.F.Seq)
			case l.A > 0:
				o.dropUpTo(l.A)
				o.seq = max(o.seq, l.A)
			case l.S > 0:
				o.seq = max(o.seq, l.S)
			}
		}
		f.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := o.compact(); err != nil {
		return nil, err
	}
	if n := len(o.pending); n > 0 {
		slog.Info("daemon: frames from before the restart will be sent to the Hub", "frames", n)
	}
	return o, nil
}

// compact rewrites the file with the seq counter and the pending frames.
// Called with mu held, or before the outbox is shared.
func (o *outbox) compact() error {
	if o.path == "" {
		return nil
	}
	if o.file != nil {
		o.file.Close()
		o.file = nil
	}
	tmp := o.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.Encode(line{S: o.seq})
	for i := range o.pending {
		enc.Encode(line{F: &o.pending[i]})
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Rename(tmp, o.path); err != nil {
		return err
	}
	o.file, err = os.OpenFile(o.path, os.O_APPEND|os.O_WRONLY, 0o600)
	o.acked = 0
	return err
}

func (o *outbox) write(l line) {
	if o.file == nil {
		return
	}
	raw, _ := json.Marshal(l)
	if _, err := o.file.Write(append(raw, '\n')); err != nil {
		slog.Warn("daemon: write outbox", "err", err)
	}
}

func (o *outbox) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.file != nil {
		o.file.Close()
		o.file = nil
	}
}

func (o *outbox) reliable(f proto.Frame) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	f.Seq = o.seq
	o.pending = append(o.pending, f)
	o.write(line{F: &f})
	if o.connected {
		o.queue = append(o.queue, f)
		o.cond.Broadcast()
	}
}

func (o *outbox) ephemeral(f proto.Frame) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.connected && len(o.queue) < maxEphemeral {
		o.queue = append(o.queue, f)
		o.cond.Broadcast()
	}
}

// connect starts a new connection: everything still pending is sent first.
func (o *outbox) connect() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.connected = true
	o.epoch++
	o.queue = append([]proto.Frame(nil), o.pending...)
	o.cond.Broadcast()
	return o.epoch
}

func (o *outbox) disconnect() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.connected = false
	o.epoch++
	o.queue = nil
	o.cond.Broadcast()
}

// next waits for the next frame of connection epoch. It returns false once
// that connection is gone.
func (o *outbox) next(ctx context.Context, epoch int) (proto.Frame, bool) {
	stop := context.AfterFunc(ctx, func() {
		o.mu.Lock()
		o.cond.Broadcast()
		o.mu.Unlock()
	})
	defer stop()
	o.mu.Lock()
	defer o.mu.Unlock()
	for len(o.queue) == 0 && o.epoch == epoch && ctx.Err() == nil {
		o.cond.Wait()
	}
	if o.epoch != epoch || ctx.Err() != nil {
		return proto.Frame{}, false
	}
	f := o.queue[0]
	o.queue = o.queue[1:]
	return f, true
}

// ack drops pending frames up to seq; the Hub stores frames in order.
func (o *outbox) ack(seq int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.dropUpTo(seq) {
		return
	}
	o.write(line{A: seq})
	o.acked++
	if o.acked >= compactAfter {
		if err := o.compact(); err != nil {
			slog.Warn("daemon: compact outbox", "err", err)
		}
	}
}

func (o *outbox) dropUpTo(seq int64) bool {
	i := 0
	for i < len(o.pending) && o.pending[i].Seq <= seq {
		i++
	}
	o.pending = o.pending[i:]
	return i > 0
}

// pendingFor reports whether an unacknowledged frame of type t mentions runID.
func (o *outbox) pendingFor(t, runID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, f := range o.pending {
		if f.T != t {
			continue
		}
		var body struct {
			RunID string `json:"run_id"`
		}
		if f.Decode(&body) == nil && body.RunID == runID {
			return true
		}
	}
	return false
}

func (o *outbox) pendingCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending)
}

// probe takes the lock, for the watchdog.
func (o *outbox) probe() {
	o.mu.Lock()
	o.mu.Unlock()
}

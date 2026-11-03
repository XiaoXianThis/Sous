package node

import (
	"context"
	"sync"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// maxEphemeral bounds queued frames that may be dropped (agent text), so a
// slow connection cannot grow the queue without limit.
const maxEphemeral = 2000

// outbox orders frames to the Hub. Reliable frames get a seq and stay
// pending until the Hub acknowledges them; after a reconnect every pending
// frame is sent again, in order. Ephemeral frames are sent only while
// connected and are never resent.
type outbox struct {
	mu        sync.Mutex
	cond      *sync.Cond
	seq       int64
	pending   []proto.Frame // sent or waiting, not yet acknowledged
	queue     []proto.Frame // to write on the current connection
	connected bool
	epoch     int // bumped on every connect and disconnect
}

func newOutbox() *outbox {
	o := &outbox{}
	o.cond = sync.NewCond(&o.mu)
	return o
}

func (o *outbox) reliable(f proto.Frame) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	f.Seq = o.seq
	o.pending = append(o.pending, f)
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
	i := 0
	for i < len(o.pending) && o.pending[i].Seq <= seq {
		i++
	}
	o.pending = o.pending[i:]
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

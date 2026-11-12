package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// A link is an ordered, two-way stream of frames between a machine and the
// brain, carried by the two one-way channels between them. It replaces the
// old WebSocket to the Hub: when anything is lost or reordered, or either
// side restarts, the link closes and the daemon reconnects, which already
// resends everything that was not acknowledged.

// Message kinds inside a channel.
type envelope struct {
	K   string          `json:"k"`
	Ep  string          `json:"ep,omitempty"`
	Op  string          `json:"op,omitempty"`
	Dir string          `json:"dir,omitempty"` // c: from the side that dialed, s: from the side that accepted
	F   json.RawMessage `json:"f,omitempty"`
}

// Link is one connection.
type Link struct {
	r      *Router
	Peer   string
	Ep     string
	side   string // c (dialed) or s (accepted)
	in     chan json.RawMessage
	done   chan struct{}
	once   sync.Once
	opened chan struct{}
	Err    error
}

// ErrLinkClosed is returned by a closed link.
var ErrLinkClosed = errors.New("link closed")

// Router dispatches decrypted messages: link traffic to links, everything
// else to Other.
type Router struct {
	C *Client
	// Accept runs for every link a peer opens (the brain's side).
	Accept func(l *Link)
	// Other gets messages that are not link traffic.
	Other func(from string, payload []byte, gap bool)

	mu    sync.Mutex
	links map[string]*Link
}

// NewRouter attaches a router to a client config before New.
func NewRouter(cfg *Config) *Router {
	r := &Router{links: map[string]*Link{}}
	cfg.Handler = r.handle
	prev := cfg.OnReset
	cfg.OnReset = func(peer string) {
		r.closePeer(peer, errors.New("peer restarted"))
		if prev != nil {
			prev(peer)
		}
	}
	return r
}

// SetAccept sets (or clears) the handler for links peers open.
func (r *Router) SetAccept(fn func(l *Link)) {
	r.mu.Lock()
	r.Accept = fn
	r.mu.Unlock()
}

func (r *Router) handle(from string, payload []byte, gap bool) {
	var e envelope
	if json.Unmarshal(payload, &e) != nil || e.K != "link" {
		if r.Other != nil {
			r.Other(from, payload, gap)
		}
		return
	}
	if gap {
		// Something between us was lost: neither side can trust the order.
		r.closePeer(from, errors.New("messages were lost"))
		return
	}
	// A message from the dialing side belongs to our accepted link, and the
	// other way round (a brain machine links to itself through the relay).
	mine := "s"
	if e.Dir == "s" {
		mine = "c"
	}
	r.mu.Lock()
	l := r.links[mine+":"+e.Ep]
	r.mu.Unlock()
	switch e.Op {
	case "open":
		r.mu.Lock()
		accept := r.Accept
		r.mu.Unlock()
		if l != nil || accept == nil {
			return
		}
		if e.Dir != "c" {
			return
		}
		// A peer opening a link may have restarted and lost our channel to
		// it: answer on a fresh one rather than wait for its reset.
		r.C.ResetOut(from)
		l = r.newLink(from, e.Ep, "s")
		close(l.opened)
		if err := r.send(from, envelope{K: "link", Ep: e.Ep, Op: "ok", Dir: "s"}); err != nil {
			l.close(err)
			return
		}
		go accept(l)
	case "ok":
		if l != nil && l.Peer == from {
			select {
			case <-l.opened:
			default:
				close(l.opened)
			}
		}
	case "frame":
		if l == nil || l.Peer != from {
			// A frame for a link we do not know: tell the peer it is gone.
			r.send(from, envelope{K: "link", Ep: e.Ep, Op: "close", Dir: map[string]string{"s": "c", "c": "s"}[mine]})
			return
		}
		select {
		case l.in <- e.F:
		case <-l.done:
		case <-time.After(30 * time.Second):
			l.close(errors.New("reader too slow"))
		}
	case "close":
		if l != nil && l.Peer == from {
			l.close(ErrLinkClosed)
		}
	}
}

func (r *Router) newLink(peer, ep, side string) *Link {
	l := &Link{r: r, Peer: peer, Ep: ep, side: side, in: make(chan json.RawMessage, 256), done: make(chan struct{}), opened: make(chan struct{})}
	r.mu.Lock()
	r.links[side+":"+ep] = l
	r.mu.Unlock()
	return l
}

func (r *Router) send(to string, e envelope) error {
	raw, _ := json.Marshal(e)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return r.C.Send(ctx, to, raw)
}

func (r *Router) closePeer(peer string, err error) {
	r.mu.Lock()
	var ls []*Link
	for _, l := range r.links {
		if l.Peer == peer {
			ls = append(ls, l)
		}
	}
	r.mu.Unlock()
	for _, l := range ls {
		l.close(err)
	}
}

// CloseAll closes every link (the brain stepped down, or the lease moved).
func (r *Router) CloseAll(err error) {
	r.mu.Lock()
	ls := make([]*Link, 0, len(r.links))
	for _, l := range r.links {
		ls = append(ls, l)
	}
	r.mu.Unlock()
	for _, l := range ls {
		l.close(err)
	}
}

// Dial opens a link to peer and waits for it to answer.
func (r *Router) Dial(ctx context.Context, peer string) (*Link, error) {
	l := r.newLink(peer, trust.B64.EncodeToString(trust.RandomBytes(12)), "c")
	if err := r.send(peer, envelope{K: "link", Ep: l.Ep, Op: "open", Dir: "c"}); err != nil {
		l.close(err)
		return nil, err
	}
	t := time.NewTimer(20 * time.Second)
	defer t.Stop()
	select {
	case <-l.opened:
		return l, nil
	case <-l.done:
		return nil, l.Err
	case <-t.C:
		l.close(errors.New("the brain did not answer"))
		return nil, l.Err
	case <-ctx.Done():
		l.close(ctx.Err())
		return nil, ctx.Err()
	}
}

// Read returns the next frame.
func (l *Link) Read(ctx context.Context) (json.RawMessage, error) {
	select {
	case f := <-l.in:
		return f, nil
	case <-l.done:
		// Drain what arrived before the close.
		select {
		case f := <-l.in:
			return f, nil
		default:
		}
		return nil, l.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Write sends a frame.
func (l *Link) Write(ctx context.Context, f json.RawMessage) error {
	select {
	case <-l.done:
		return l.Err
	default:
	}
	raw, _ := json.Marshal(envelope{K: "link", Ep: l.Ep, Op: "frame", Dir: l.side, F: f})
	if err := l.r.C.Send(ctx, l.Peer, raw); err != nil {
		l.close(err)
		return err
	}
	return nil
}

// Close ends the link and tells the peer.
func (l *Link) Close() {
	select {
	case <-l.done:
		return
	default:
	}
	l.r.send(l.Peer, envelope{K: "link", Ep: l.Ep, Op: "close", Dir: l.side})
	l.close(ErrLinkClosed)
}

// Done is closed when the link ends.
func (l *Link) Done() <-chan struct{} { return l.done }

func (l *Link) close(err error) {
	l.once.Do(func() {
		if err == nil {
			err = ErrLinkClosed
		}
		l.Err = err
		close(l.done)
		l.r.mu.Lock()
		if l.r.links[l.side+":"+l.Ep] == l {
			delete(l.r.links, l.side+":"+l.Ep)
		}
		l.r.mu.Unlock()
		slog.Debug("tunnel: link closed", "peer", l.Peer, "err", err)
	})
}

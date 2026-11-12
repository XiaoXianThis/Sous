// Package brain runs the thinking part of sous on a machine the user
// designated (decision 0031): the Hub of 0.2, now inside the daemon, behind
// the relay. Brain machines elect one holder by lease; the holder restores
// the newest encrypted snapshot, runs the Hub on an in-memory listener and
// answers the pages and machines over end-to-end encrypted channels.
package brain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
)

// Brain is one brain machine's side of the election and, while it holds
// the lease, the running Hub.
type Brain struct {
	Root   string // the machine's sous home
	NodeID string
	C      *tunnel.Client
	R      *tunnel.Router
	// Origin is the page's origin (push notifications name it).
	Origin string
	// SnapshotEvery is how often a changed state is uploaded; Tick how
	// often the election runs.
	SnapshotEvery time.Duration
	Tick          time.Duration

	mu      sync.Mutex
	leading bool
	term    int64
	h       *hub.Hub
	ln      *memListener
	stop    context.CancelFunc
	done    chan struct{}
	streams map[string]context.CancelFunc
	snapSeq int64
	lastEv  int64
	kick    chan struct{}
	runDone chan struct{}
	noLease time.Time
}

func (b *Brain) clearWait() {
	b.mu.Lock()
	b.noLease = time.Time{}
	b.mu.Unlock()
}

func (b *Brain) finished() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runDone == nil {
		b.runDone = make(chan struct{})
	}
	return b.runDone
}

// Done is closed when Run returned (after the last snapshot).
func (b *Brain) Done() <-chan struct{} { return b.finished() }

// Dir is where the brain keeps its state on this machine.
func Dir(root string) string { return filepath.Join(root, "brain") }

// DataDir is the Hub's data directory inside Dir.
func DataDir(root string) string { return filepath.Join(Dir(root), "data") }

// Run takes part in the election until ctx ends.
func (b *Brain) Run(ctx context.Context) error {
	defer close(b.finished())
	if b.SnapshotEvery == 0 {
		b.SnapshotEvery = time.Minute
	}
	b.mu.Lock()
	b.streams = map[string]context.CancelFunc{}
	b.kick = make(chan struct{}, 1)
	b.mu.Unlock()
	b.R.SetAccept(b.acceptLink)
	if err := os.MkdirAll(Dir(b.Root), 0o700); err != nil {
		return err
	}
	b.snapSeq = readLocalSeq(b.Root)
	select {
	case <-b.C.Ready():
	case <-ctx.Done():
		return nil
	}
	if b.Tick == 0 {
		b.Tick = 5 * time.Second
	}
	t := time.NewTicker(b.Tick)
	defer t.Stop()
	snap := time.NewTicker(b.SnapshotEvery)
	defer snap.Stop()
	forced := time.Now()
	for {
		b.step(ctx)
		select {
		case <-ctx.Done():
			b.stepDown("shutting down", true)
			return nil
		case <-t.C:
		case <-b.kick:
		case <-snap.C:
			if b.Leading() {
				force := time.Since(forced) > 10*time.Minute
				if err := b.snapshot(ctx, force); err != nil {
					slog.Warn("brain: snapshot failed", "err", err)
				} else if force {
					forced = time.Now()
				}
			}
		}
	}
}

// Kick re-runs the election step soon (a document changed).
func (b *Brain) Kick() {
	b.mu.Lock()
	k := b.kick
	b.mu.Unlock()
	if k == nil {
		return
	}
	select {
	case k <- struct{}{}:
	default:
	}
}

// Leading reports whether this machine holds the lease and runs the Hub.
func (b *Brain) Leading() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.leading
}

// Hub is the running Hub (nil unless leading).
func (b *Brain) Hub() *hub.Hub {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.h
}

func (b *Brain) step(ctx context.Context) {
	r := b.C.Roster()
	if r == nil {
		return
	}
	me, ok := r.Node(b.NodeID)
	if !ok || me.Brain == nil || me.Frozen {
		if b.Leading() {
			b.stepDown("no longer a brain machine", false)
		}
		return
	}
	now := b.C.Now()
	l, _, has := b.C.Lease()
	switch {
	case has && l.Live(now) && l.Holder == b.NodeID:
		b.clearWait()
		if !b.Leading() {
			b.lead(ctx, l.Term)
		}
		if time.Unix(l.Exp, 0).Sub(now) < trust.LeaseTTL-trust.LeaseRenew {
			b.claim(ctx, l.Term)
		}
	case has && l.Live(now):
		b.clearWait()
		if b.Leading() {
			b.stepDown(fmt.Sprintf("%s holds term %d", l.Holder, l.Term), false)
		}
	default:
		// No live lease: the first brain machine online by priority claims
		// it; if that one does not within a while, the next one does.
		b.mu.Lock()
		if b.noLease.IsZero() {
			b.noLease = now
		}
		waited := now.Sub(b.noLease)
		b.mu.Unlock()
		online := map[string]bool{}
		for _, a := range b.C.Online() {
			online[a] = true
		}
		online[trust.NodeAddr(b.NodeID)] = true
		for _, n := range r.Brains() {
			if !online[trust.NodeAddr(n.ID)] {
				continue
			}
			if n.ID != b.NodeID && waited < 2*trust.LeaseTTL {
				return // a machine before us will claim
			}
			term := int64(1)
			if has {
				term = l.Term + 1
			}
			if b.Leading() && b.term != term {
				b.stepDown("lease expired", false)
			}
			if b.claim(ctx, term) {
				b.lead(ctx, term)
			}
			return
		}
	}
}

func (b *Brain) claim(ctx context.Context, term int64) bool {
	now := b.C.Now()
	l := trust.Lease{T: trust.TLease, Term: term, Holder: b.NodeID, IAT: now.Unix(), Exp: now.Add(trust.LeaseTTL).Unix()}
	doc, err := trust.Sign(b.C.SignKey(), trust.TLease, l)
	if err != nil {
		return false
	}
	if err := b.C.Put(ctx, "lease", doc); err != nil {
		slog.Info("brain: could not take the lease", "term", term, "err", err)
		return false
	}
	b.C.SetLease(l, doc)
	return true
}

func (b *Brain) lead(ctx context.Context, term int64) {
	if err := b.restore(ctx); err != nil {
		slog.Error("brain: cannot restore the newest snapshot; not taking over", "err", err)
		return
	}
	r := b.C.Roster()
	cfg, err := config.ForBrain(DataDir(b.Root), b.origin(r), filepath.Join(b.Root, "brain.yaml"))
	if err != nil {
		slog.Error("brain: config", "err", err)
		return
	}
	h, err := hub.Open(cfg)
	if err != nil {
		slog.Error("brain: cannot open the Hub", "err", err)
		return
	}
	ln := newMemListener()
	h.Brain, h.ConnContext, h.Started = true, connContext, make(chan struct{})
	hctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := h.RunOn(hctx, ln); err != nil {
			slog.Error("brain: the Hub stopped", "err", err)
		}
	}()
	select {
	case <-h.Started:
	case <-done:
		cancel()
		return
	}
	b.mu.Lock()
	b.leading, b.term, b.h, b.ln, b.stop, b.done = true, term, h, ln, cancel, done
	b.mu.Unlock()
	b.syncRoster(ctx)
	slog.Info("brain: this machine now thinks for everyone", "term", term)
}

func (b *Brain) origin(r *trust.Roster) string {
	if b.Origin != "" {
		return b.Origin
	}
	if r != nil {
		for _, o := range r.Origins {
			if len(o) > 8 && o[:8] == "https://" {
				return o
			}
		}
	}
	return ""
}

// stepDown stops the Hub. final uploads one last snapshot first.
func (b *Brain) stepDown(why string, final bool) {
	b.mu.Lock()
	if !b.leading {
		b.mu.Unlock()
		return
	}
	stop, done := b.stop, b.done
	b.mu.Unlock()
	if final {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := b.snapshot(ctx, false); err != nil {
			slog.Warn("brain: last snapshot failed", "err", err)
		}
		cancel()
	}
	b.mu.Lock()
	b.leading = false
	for k, c := range b.streams {
		c()
		delete(b.streams, k)
	}
	b.mu.Unlock()
	stop()
	<-done
	b.mu.Lock()
	b.h, b.ln = nil, nil
	b.mu.Unlock()
	b.R.CloseAll(errors.New("the brain stepped down"))
	slog.Info("brain: stepped down", "why", why)
}

// syncRoster mirrors the roster into the Hub's machines: new ones are
// added, kicked ones revoked (and disconnected).
func (b *Brain) syncRoster(ctx context.Context) {
	h := b.Hub()
	r := b.C.Roster()
	if h == nil || r == nil {
		return
	}
	known := map[string]bool{}
	nodes, _ := h.Store.Nodes(ctx)
	for _, n := range nodes {
		if !n.Revoked {
			known[n.ID] = true
		}
	}
	in := map[string]bool{}
	for _, n := range r.Nodes {
		in[n.ID] = true
		if known[n.ID] {
			continue
		}
		if _, err := h.Log.Append(ctx, event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{
			NodeID: n.ID, Name: n.Name, OS: n.OS})); err != nil {
			slog.Warn("brain: cannot add a machine", "node", n.ID, "err", err)
		}
	}
	for id := range known {
		if !in[id] {
			if err := h.Nodes.Revoke(ctx, id); err != nil {
				slog.Warn("brain: cannot remove a machine", "node", id, "err", err)
			}
		}
	}
}

// RosterChanged is called by the daemon when a new roster arrived.
func (b *Brain) RosterChanged() {
	b.Kick()
	if b.Leading() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		b.syncRoster(ctx)
		// Machines that left lose their links at once.
		r := b.C.Roster()
		if r != nil {
			for _, a := range b.C.Online() {
				if id, ok := cutNode(a); ok {
					if n, in := r.Node(id); !in || n.Frozen {
						b.C.ForgetInbound(a)
					}
				}
			}
		}
	}
}

func cutNode(a string) (string, bool) {
	if len(a) > 5 && a[:5] == "node:" {
		return a[5:], true
	}
	return "", false
}

// memListener hands in-memory connections to the Hub's HTTP server; each
// carries who sent the request (connContext).
type memListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newMemListener() *memListener {
	return &memListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

type taggedConn struct {
	net.Conn
	tun auth.Tunnel
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "brain" }

func (l *memListener) dial(ctx context.Context, t auth.Tunnel) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.ch <- &taggedConn{Conn: server, tun: t}:
		return client, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func connContext(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*taggedConn); ok {
		return auth.WithTunnel(ctx, tc.tun)
	}
	return ctx
}

// client is an HTTP client whose every connection reaches the Hub as t.
func (b *Brain) client(t auth.Tunnel) (*http.Client, error) {
	b.mu.Lock()
	ln := b.ln
	b.mu.Unlock()
	if ln == nil {
		return nil, errNotLeading
	}
	return &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return ln.dial(ctx, t) },
		DisableKeepAlives: true,
	}}, nil
}

var errNotLeading = errors.New("这台机器现在不是大脑")

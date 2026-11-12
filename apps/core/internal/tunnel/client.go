// Package tunnel is a machine's side of the relay: it authenticates, keeps
// the signed documents (roster, scopes, pause, lease, domain keys) after
// checking them against the pinned identity, and sends and receives
// end-to-end encrypted messages over trust channels. Nothing the relay says
// is believed without a signature (work/specs/zero-trust).
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// Config sets up a client.
type Config struct {
	URL      string // relay base URL (https://, wss://, http:// or ws://)
	Addr     string // node:<id> or sess:<sid>
	Sign     *trust.SignKey
	KEM      *trust.KEMKey
	Identity []byte        // the user's identity signing key, pinned at pairing
	Cert     *trust.Signed // a session's certificate (tests act as sessions)
	StateDir string        // where versions and seen channels persist; "" keeps them in memory
	Now      func() time.Time

	// Handler gets every decrypted message. gap reports lost or reordered
	// messages before this one on its channel.
	Handler func(from string, payload []byte, gap bool)
	// OnDoc runs after a document changed (already verified).
	OnDoc func(name string)
	// OnReset runs when a peer lost our channel (it restarted).
	OnReset func(peer string)
	// OnPresence runs when the relay reports who is online (untrusted).
	OnPresence func(online []string)
	// OnConnect runs after each successful authentication.
	OnConnect func()
}

// Client is a connection to the relay that survives reconnects.
type Client struct {
	cfg Config

	mu        sync.Mutex
	ws        *websocket.Conn
	wmu       sync.Mutex
	smu       sync.Mutex // keeps sealing and writing in the same order
	pending   map[string]chan rframe
	rid       atomic.Int64
	out       map[string]*trust.OutChannel
	in        map[string]*inbound
	certs     map[string]trust.SessionCert
	online    []string
	connected bool
	ready     chan struct{}
	readyOnce sync.Once

	st       state
	roster   *trust.Roster
	scopes   *trust.Scopes
	pause    *trust.Pause
	lease    *trust.Lease
	leaseDoc trust.Signed
	domain   *trust.DomainKeys
	snapshot *trust.SnapshotManifest
	snapDoc  trust.Signed
	usedAsrt map[string]time.Time
	rooms    map[string]chan trust.PairMsg
}

type inbound struct {
	ch   *trust.InChannel
	from string
	at   time.Time // when it was accepted
}

// state is what persists across restarts.
type state struct {
	Roster *trust.RosterUpdate `json:"roster,omitempty"`
	Scopes *trust.ScopesUpdate `json:"scopes,omitempty"`
	Pause  *trust.Signed       `json:"pause,omitempty"`
	Domain *trust.Signed       `json:"domain,omitempty"`
	Seen   map[string]int64    `json:"seen,omitempty"` // channel id -> opening time
	Snap   int64               `json:"snap,omitempty"` // newest snapshot seq seen
}

type rframe struct {
	T     string          `json:"t"`
	RID   string          `json:"rid,omitempty"`
	Nonce string          `json:"nonce,omitempty"`
	Addr  string          `json:"addr,omitempty"`
	Pub   string          `json:"pub,omitempty"`
	Sig   string          `json:"sig,omitempty"`
	Cert  *trust.Signed   `json:"cert,omitempty"`
	Token string          `json:"token,omitempty"`
	To    string          `json:"to,omitempty"`
	From  string          `json:"from,omitempty"`
	ID    int64           `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Room  string          `json:"room,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
	Blob  string          `json:"blob,omitempty"`
	Error string          `json:"error,omitempty"`
	Live  []string        `json:"online,omitempty"`
}

// control is a message outside any channel; only "reset" exists.
type control struct {
	Reset string `json:"reset,omitempty"`
}

// ErrOffline is returned while the relay is unreachable.
var ErrOffline = errors.New("连不上中继")

// New makes a client; Run connects it.
func New(cfg Config) (*Client, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	c := &Client{cfg: cfg, pending: map[string]chan rframe{}, out: map[string]*trust.OutChannel{}, in: map[string]*inbound{},
		certs: map[string]trust.SessionCert{}, ready: make(chan struct{}), usedAsrt: map[string]time.Time{}}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// WSURL turns a relay base URL into its websocket endpoint.
func WSURL(base string) string {
	u := strings.TrimRight(base, "/")
	u = strings.Replace(u, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	if !strings.HasSuffix(u, "/v1/ws") {
		u += "/v1/ws"
	}
	return u
}

// Run keeps the connection up until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		slog.Debug("tunnel: disconnected", "addr", c.cfg.Addr, "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// Ready is closed after the first connection has loaded the documents.
func (c *Client) Ready() <-chan struct{} { return c.ready }

// Connected reports whether the relay is reachable now.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *Client) session(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ws, _, err := websocket.Dial(dctx, WSURL(c.cfg.URL), nil)
	cancel()
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(9 << 20)
	read := func(ctx context.Context) (rframe, error) {
		var f rframe
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return f, err
		}
		return f, json.Unmarshal(raw, &f)
	}
	actx, done := context.WithTimeout(ctx, 20*time.Second)
	defer done()
	f, err := read(actx)
	if err != nil || f.T != "challenge" {
		return fmt.Errorf("relay: expected a challenge: %v", err)
	}
	nonce, _ := trust.B64.DecodeString(f.Nonce)
	auth := rframe{T: "auth", Addr: c.cfg.Addr, Pub: trust.B64.EncodeToString(c.cfg.Sign.Public()),
		Sig: trust.B64.EncodeToString(c.cfg.Sign.Sign("sous/v1/relay-auth", nonce)), Cert: c.cfg.Cert}
	if err := writeJSON(actx, ws, auth); err != nil {
		return err
	}
	if f, err = read(actx); err != nil {
		return err
	}
	if f.T != "welcome" {
		return fmt.Errorf("relay refused: %s", f.Error)
	}
	done()

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	c.mu.Lock()
	c.ws, c.connected = ws, true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.ws, c.connected = nil, false
		for rid, ch := range c.pending {
			close(ch)
			delete(c.pending, rid)
		}
		c.mu.Unlock()
	}()
	go func() {
		c.sync(sctx)
		c.readyOnce.Do(func() { close(c.ready) })
		if c.cfg.OnConnect != nil {
			c.cfg.OnConnect()
		}
	}()
	go c.keepalive(sctx, ws)
	for {
		f, err := read(sctx)
		if err != nil {
			return err
		}
		c.dispatch(sctx, f)
	}
}

func (c *Client) keepalive(ctx context.Context, ws *websocket.Conn) {
	t := time.NewTicker(25 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// A ping waits for the write in progress; give it room on a slow uplink.
			pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				ws.CloseNow()
				return
			}
		}
	}
}

func writeJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, raw)
}

// writeTimeout allows a frame 30 seconds plus one second per 16 KB, so a
// large frame on a slow uplink (around 100 KB/s) is not cut off: a write
// that times out closes the whole connection.
func writeTimeout(f rframe) time.Duration {
	n := len(f.Data) + len(f.Blob) + len(f.Body)
	return 30*time.Second + time.Duration(n/(16<<10))*time.Second
}

func (c *Client) write(ctx context.Context, f rframe) error {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return ErrOffline
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	// A request may expire while queued. Passing that context to Write
	// would close the shared socket and interrupt unrelated requests.
	if err := ctx.Err(); err != nil {
		return err
	}
	// Once a frame starts, finish it within its own bounded timeout. A caller
	// canceling midway must not tear down every user's shared connection.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout(f))
	defer cancel()
	return writeJSON(wctx, ws, f)
}

// request sends f and waits for the frame answering it.
func (c *Client) request(ctx context.Context, f rframe) (rframe, error) {
	f.RID = strconv.FormatInt(c.rid.Add(1), 10)
	ch := make(chan rframe, 1)
	c.mu.Lock()
	c.pending[f.RID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, f.RID)
		c.mu.Unlock()
	}()
	if err := c.write(ctx, f); err != nil {
		return rframe{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case r, ok := <-ch:
		if !ok {
			return r, ErrOffline
		}
		if r.T == "err" {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-rctx.Done():
		return rframe{}, rctx.Err()
	}
}

func (c *Client) dispatch(ctx context.Context, f rframe) {
	if f.RID != "" {
		c.mu.Lock()
		ch := c.pending[f.RID]
		c.mu.Unlock()
		if ch != nil {
			ch <- f
			return
		}
	}
	switch f.T {
	case "msg":
		c.receive(ctx, f)
	case "doc":
		if !strings.Contains(f.Name, "@") {
			c.acceptDoc(ctx, f.Name, f.Body)
		}
	case "presence":
		c.mu.Lock()
		c.online = f.Live
		c.mu.Unlock()
		if c.cfg.OnPresence != nil {
			c.cfg.OnPresence(f.Live)
		}
	case "room":
		c.roomMsg(f)
	}
}

// sync loads every document after (re)connecting.
func (c *Client) sync(ctx context.Context) {
	for _, name := range []string{"roster", "scopes", "pause", "domain", "lease", "snapshot"} {
		r, err := c.request(ctx, rframe{T: "get", Name: name})
		if err != nil {
			slog.Warn("tunnel: cannot load a document", "name", name, "err", err)
			continue
		}
		if len(r.Body) > 0 {
			c.acceptDoc(ctx, name, r.Body)
		}
	}
	if r, err := c.request(ctx, rframe{T: "presence"}); err == nil {
		c.mu.Lock()
		c.online = r.Live
		c.mu.Unlock()
		if c.cfg.OnPresence != nil {
			c.cfg.OnPresence(r.Live)
		}
	}
}

// Online lists the addresses the relay says are connected (untrusted).
func (c *Client) Online() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.online...)
}

// Get reads a raw document (callers verify what they use).
func (c *Client) Get(ctx context.Context, name string) (json.RawMessage, error) {
	r, err := c.request(ctx, rframe{T: "get", Name: name})
	return r.Body, err
}

// Put writes a document.
func (c *Client) Put(ctx context.Context, name string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, rframe{T: "put", Name: name, Body: raw})
	return err
}

// BlobPut stores bytes under name.
func (c *Client) BlobPut(ctx context.Context, name string, data []byte) error {
	_, err := c.request(ctx, rframe{T: "blob.put", Name: name, Blob: trust.B64.EncodeToString(data)})
	return err
}

// BlobKeep deletes every blob under prefix except the named ones (only the
// lease holder may do it for snapshot/).
func (c *Client) BlobKeep(ctx context.Context, prefix string, keep []string) error {
	body, err := json.Marshal(keep)
	if err != nil {
		return err
	}
	_, err = c.request(ctx, rframe{T: "blob.keep", Name: prefix, Body: body})
	return err
}

// BlobGet reads bytes; empty when missing.
func (c *Client) BlobGet(ctx context.Context, name string) ([]byte, error) {
	r, err := c.request(ctx, rframe{T: "blob.get", Name: name})
	if err != nil {
		return nil, err
	}
	return trust.B64.DecodeString(r.Blob)
}

// Send encrypts payload to an address over this client's channel to it.
func (c *Client) Send(ctx context.Context, to string, payload []byte) error {
	c.smu.Lock()
	defer c.smu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	oc := c.out[to]
	if oc != nil && oc.Stale(c.cfg.Now()) {
		// Time for a fresh key: the next message opens a new channel.
		delete(c.out, to)
		oc = nil
	}
	c.mu.Unlock()
	if oc == nil {
		kem, err := c.kemOf(to)
		if err != nil {
			return err
		}
		oc, err = trust.NewOutChannel(c.cfg.Addr, to, c.cfg.Sign, kem, c.cfg.Now())
		if err != nil {
			return err
		}
		c.mu.Lock()
		if cur := c.out[to]; cur != nil {
			oc = cur
		} else {
			c.out[to] = oc
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	w := oc.Seal(payload)
	c.mu.Unlock()
	if w.Open != nil && c.cfg.Cert != nil {
		w.Cert = c.cfg.Cert
	}
	data, _ := json.Marshal(w)
	if err := c.write(ctx, rframe{T: "send", To: to, Data: data}); err != nil {
		// The message is lost; start a new channel so the next one opens it.
		c.ResetOut(to)
		return err
	}
	return nil
}

// ResetOut forgets the channel to an address; the next Send opens a new one.
func (c *Client) ResetOut(to string) {
	c.mu.Lock()
	delete(c.out, to)
	c.mu.Unlock()
}

func (c *Client) kemOf(addr string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := strings.CutPrefix(addr, "node:"); ok {
		if c.roster == nil {
			return nil, errors.New("还没有名册")
		}
		n, ok := c.roster.Node(id)
		if !ok {
			return nil, fmt.Errorf("名册里没有 %s", id)
		}
		return trust.B64.DecodeString(n.KEMPub)
	}
	if sid, ok := strings.CutPrefix(addr, "sess:"); ok {
		cert, ok := c.certs[sid]
		if !ok {
			return nil, fmt.Errorf("不认识会话 %s", sid)
		}
		return trust.B64.DecodeString(cert.KEMPub)
	}
	return nil, fmt.Errorf("未知的地址 %q", addr)
}

// SessionCert returns a session's certificate this client has seen.
func (c *Client) SessionCert(sid string) (trust.SessionCert, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cert, ok := c.certs[sid]
	return cert, ok
}

// SenderOK reports whether an address may still talk to us: a machine in
// the roster that is not frozen, or a session whose certificate is valid.
func (c *Client) SenderOK(addr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := strings.CutPrefix(addr, "node:"); ok {
		if c.roster == nil {
			return false
		}
		n, ok := c.roster.Node(id)
		return ok && !n.Frozen
	}
	if sid, ok := strings.CutPrefix(addr, "sess:"); ok {
		cert, ok := c.certs[sid]
		return ok && c.cfg.Now().Unix() <= cert.Exp+int64(trust.ClockSkew/time.Second)
	}
	return false
}

func (c *Client) receive(ctx context.Context, f rframe) {
	defer func() {
		if f.ID > 0 {
			c.write(ctx, rframe{T: "ack", ID: f.ID})
		}
	}()
	var w trust.Wire
	if err := json.Unmarshal(f.Data, &w); err != nil {
		return
	}
	if w.Ch == "" {
		var ctl control
		if json.Unmarshal(f.Data, &ctl) == nil && ctl.Reset != "" {
			c.mu.Lock()
			oc := c.out[f.From]
			if oc != nil && oc.ID == ctl.Reset {
				delete(c.out, f.From)
			}
			c.mu.Unlock()
			if oc != nil && oc.ID == ctl.Reset && c.cfg.OnReset != nil {
				c.cfg.OnReset(f.From)
			}
		}
		return
	}
	c.mu.Lock()
	ib := c.in[w.Ch]
	c.mu.Unlock()
	if ib == nil {
		if w.Open == nil {
			// We lost this channel (restart): ask the sender to open a new one.
			raw, _ := json.Marshal(control{Reset: w.Ch})
			c.write(ctx, rframe{T: "send", To: f.From, Data: raw})
			return
		}
		var err error
		if ib, err = c.accept(w); err != nil {
			slog.Warn("tunnel: rejected a channel", "from", f.From, "err", err)
			return
		}
	}
	if !c.SenderOK(ib.from) {
		slog.Warn("tunnel: dropped a message from a sender that may no longer talk", "from", ib.from)
		return
	}
	c.mu.Lock()
	pt, err := ib.ch.Open(w)
	c.mu.Unlock()
	gap := errors.Is(err, trust.ErrGap)
	if err != nil && !gap {
		slog.Warn("tunnel: dropped a message", "from", ib.from, "err", err)
		return
	}
	if c.cfg.Handler != nil {
		c.cfg.Handler(ib.from, pt, gap)
	}
}

func (c *Client) accept(w trust.Wire) (*inbound, error) {
	o, err := trust.PeekOpen(*w.Open)
	if err != nil {
		return nil, err
	}
	if o.Ch != w.Ch {
		return nil, errors.New("opening names another channel")
	}
	var pub []byte
	switch {
	case strings.HasPrefix(o.From, "node:"):
		c.mu.Lock()
		r := c.roster
		c.mu.Unlock()
		if r == nil {
			return nil, errors.New("no roster yet")
		}
		n, ok := r.Node(strings.TrimPrefix(o.From, "node:"))
		if !ok || n.Frozen {
			return nil, fmt.Errorf("%s is not an active machine", o.From)
		}
		pub, _ = trust.B64.DecodeString(n.SignPub)
	case strings.HasPrefix(o.From, "sess:"):
		if w.Cert == nil {
			return nil, errors.New("a session opened a channel without its certificate")
		}
		c.mu.Lock()
		r := c.roster
		c.mu.Unlock()
		if r == nil {
			return nil, errors.New("no roster yet")
		}
		cert, err := trust.OpenSessionCert(r, *w.Cert, c.cfg.Now())
		if err != nil {
			return nil, err
		}
		if trust.SessionAddr(cert.SID) != o.From {
			return nil, errors.New("certificate of another session")
		}
		pub, _ = trust.B64.DecodeString(cert.SignPub)
		c.mu.Lock()
		c.certs[cert.SID] = cert
		c.mu.Unlock()
	default:
		return nil, fmt.Errorf("unknown sender %q", o.From)
	}
	c.mu.Lock()
	_, seen := c.st.Seen[o.Ch]
	c.mu.Unlock()
	if seen {
		return nil, errors.New("this channel was opened before (replay)")
	}
	in, err := trust.AcceptChannel(*w.Open, pub, c.cfg.Addr, c.cfg.KEM, c.cfg.Now())
	if err != nil {
		return nil, err
	}
	now := c.cfg.Now()
	ib := &inbound{ch: in, from: o.From, at: now}
	c.mu.Lock()
	// The sender rekeyed: keep its previous channel a while for messages
	// still on the way, and forget older ones with their keys.
	for id, old := range c.in {
		if old.from == o.From && now.Sub(old.at) > ChanGrace {
			newest := true
			for _, x := range c.in {
				if x != old && x.from == o.From && x.at.After(old.at) {
					newest = false
				}
			}
			if !newest {
				delete(c.in, id)
			}
		}
	}
	c.in[w.Ch] = ib
	if c.st.Seen == nil {
		c.st.Seen = map[string]int64{}
	}
	cut := c.cfg.Now().Add(-trust.ChanOpenTTL - 2*trust.ClockSkew).Unix()
	for k, t := range c.st.Seen {
		if t < cut {
			delete(c.st.Seen, k)
		}
	}
	c.st.Seen[o.Ch] = o.IAT
	c.mu.Unlock()
	c.save()
	return ib, nil
}

// ChanGrace is how long a sender's replaced channel is still read.
const ChanGrace = 10 * time.Minute

// Channels counts the open channels (for diagnostics and tests).
func (c *Client) Channels() (in, out int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.in), len(c.out)
}

// ForgetInbound drops channels from a sender (it was kicked or frozen).
func (c *Client) ForgetInbound(from string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ib := range c.in {
		if ib.from == from {
			delete(c.in, id)
		}
	}
	delete(c.out, from)
}

func (c *Client) load() error {
	if c.cfg.StateDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(c.cfg.StateDir, "tunnel.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	now := c.cfg.Now()
	if st.Roster != nil {
		r, err := trust.OpenRoster(c.cfg.Identity, st.Roster.Doc)
		if err != nil {
			return fmt.Errorf("tunnel: the saved roster does not verify: %w", err)
		}
		c.roster = &r
		c.st.Roster = st.Roster
	}
	if st.Scopes != nil && c.roster != nil {
		if sc, err := st.Scopes.Open(c.cfg.Identity); err == nil {
			c.scopes, c.st.Scopes = &sc, st.Scopes
		}
	}
	if st.Pause != nil {
		var p trust.Pause
		if _, err := st.Pause.Open(c.cfg.Identity, trust.TPause, &p); err == nil {
			c.pause, c.st.Pause = &p, st.Pause
		}
	}
	if st.Domain != nil {
		var d trust.DomainKeys
		if _, err := st.Domain.Open(c.cfg.Identity, trust.TDomain, &d); err == nil {
			c.domain, c.st.Domain = &d, st.Domain
		}
	}
	c.st.Seen, c.st.Snap = st.Seen, st.Snap
	_ = now
	return nil
}

func (c *Client) save() {
	if c.cfg.StateDir == "" {
		return
	}
	c.mu.Lock()
	raw, err := json.Marshal(c.st)
	c.mu.Unlock()
	if err != nil {
		return
	}
	tmp := filepath.Join(c.cfg.StateDir, "tunnel.json.tmp")
	if os.WriteFile(tmp, raw, 0o600) == nil {
		os.Rename(tmp, filepath.Join(c.cfg.StateDir, "tunnel.json"))
	}
}

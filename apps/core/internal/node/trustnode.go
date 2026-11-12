package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
)

// The zero-trust side of a machine (decision 0030): it reaches the brain
// only through the relay, accepts frames only from the machine holding the
// current lease, checks each against the user's autonomy scope and permits
// (guard.go), and logs what it accepted and refused (audit.go).

// Keys is a machine's own key pair, in trust/keys.json (0600).
type Keys struct {
	Sign    string `json:"sign"` // P-256 private scalar
	KEM     string `json:"kem"`  // X-Wing seed
	Storage string `json:"storage"`
}

func trustDir(root string) string { return filepath.Join(root, "trust") }

// LoadKeys reads the machine's keys.
func LoadKeys(root string) (*trust.SignKey, *trust.KEMKey, error) {
	raw, err := os.ReadFile(filepath.Join(trustDir(root), "keys.json"))
	if err != nil {
		return nil, nil, err
	}
	var k Keys
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, nil, err
	}
	sb, err1 := trust.B64.DecodeString(k.Sign)
	kb, err2 := trust.B64.DecodeString(k.KEM)
	if err := errors.Join(err1, err2); err != nil {
		return nil, nil, err
	}
	s, err := trust.SignKeyFromBytes(sb)
	if err != nil {
		return nil, nil, err
	}
	kem, err := trust.KEMKeyFromBytes(kb)
	return s, kem, err
}

func saveKeys(root string, s *trust.SignKey, k *trust.KEMKey) error {
	if err := os.MkdirAll(trustDir(root), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(Keys{Sign: trust.B64.EncodeToString(s.Bytes()), KEM: trust.B64.EncodeToString(k.Bytes()), Storage: "software"}, "", "  ")
	return os.WriteFile(filepath.Join(trustDir(root), "keys.json"), raw, 0o600)
}

// ErrNeedsPairing means node.json is from 0.2 or keys are missing.
var ErrNeedsPairing = errors.New("这台机器还没有按 0.3 的方式配对：在页面上点“添加机器”，把配对串粘贴到 `sous pair <中继地址> <配对串>`")

type nodeTrust struct {
	d      *Daemon
	c      *tunnel.Client
	r      *tunnel.Router
	rpc    *tunnel.RPCClient
	audit  *auditLog
	leases chan struct{}

	mu         sync.Mutex
	permits    map[string]trust.Permit // id -> unused permit
	jobOwners  map[string]string       // job id -> session that permitted it
	reqOwners  map[string]string       // node.request id -> session that permitted it
	violations []time.Time
	frozen     *frozenState

	// OtherSession lets the brain module see session traffic on this
	// machine's address (the brain and the machine share one address).
	OtherSession func(from string, payload []byte)
	// OtherNode lets the brain see non-link traffic from machines.
	OtherNode func(from string, payload []byte)
	docHooks  []func(name string)
	cancel    context.CancelFunc
	removed   bool
}

// OnTrustDoc runs fn after each verified document change (roster, lease...).
func (d *Daemon) OnTrustDoc(fn func(name string)) {
	d.mu.Lock()
	nt := d.trust
	d.mu.Unlock()
	if nt == nil {
		return
	}
	nt.mu.Lock()
	nt.docHooks = append(nt.docHooks, fn)
	nt.mu.Unlock()
}

type frozenState struct {
	Reason string `json:"reason"`
	At     int64  `json:"at"`
}

func (d *Daemon) openTrust() (Transport, error) {
	cfg := d.conf()
	if cfg.RelayURL == "" || cfg.Identity == "" {
		return nil, ErrNeedsPairing
	}
	sign, kem, err := LoadKeys(d.root)
	if err != nil {
		return nil, fmt.Errorf("%w（%v）", ErrNeedsPairing, err)
	}
	id, err := trust.B64.DecodeString(cfg.Identity)
	if err != nil {
		return nil, err
	}
	nt := &nodeTrust{d: d, leases: make(chan struct{}, 1), permits: map[string]trust.Permit{}, jobOwners: map[string]string{}, reqOwners: map[string]string{}}
	nt.audit, err = openAudit(filepath.Join(trustDir(d.root), "audit.log"))
	if err != nil {
		return nil, err
	}
	if raw, err := os.ReadFile(filepath.Join(trustDir(d.root), "frozen.json")); err == nil {
		var f frozenState
		if json.Unmarshal(raw, &f) == nil && f.Reason != "" {
			nt.frozen = &f
		}
	}
	tcfg := tunnel.Config{URL: cfg.RelayURL, Addr: trust.NodeAddr(cfg.NodeID), Sign: sign, KEM: kem, Identity: id, StateDir: trustDir(d.root),
		OnDoc: func(name string) {
			if name == "lease" || name == "roster" {
				select {
				case nt.leases <- struct{}{}:
				default:
				}
			}
			if name == "roster" {
				nt.rosterChanged()
			}
			nt.mu.Lock()
			hooks := nt.docHooks
			nt.mu.Unlock()
			for _, fn := range hooks {
				fn(name)
			}
		}}
	nt.r = tunnel.NewRouter(&tcfg)
	nt.r.Other = nt.other
	nt.c, err = tunnel.New(tcfg)
	if err != nil {
		return nil, err
	}
	nt.r.C = nt.c
	nt.rpc = tunnel.NewRPCClient(nt.c)
	tctx, cancel := context.WithCancel(context.Background())
	nt.cancel = cancel
	go nt.c.Run(tctx)
	d.mu.Lock()
	d.trust = nt
	d.mu.Unlock()
	return nt, nil
}

// Tunnel exposes the machine's relay client and router, for the brain
// module that runs on brain machines (internal/brain).
func (d *Daemon) Tunnel() (*tunnel.Client, *tunnel.Router, error) {
	if _, err := d.transport(); err != nil {
		return nil, nil, err
	}
	d.mu.Lock()
	nt := d.trust
	d.mu.Unlock()
	if nt == nil {
		return nil, nil, errors.New("this daemon does not use the relay")
	}
	return nt.c, nt.r, nil
}

// SetBrainHooks routes session and machine messages to the brain module.
func (d *Daemon) SetBrainHooks(session, machine func(from string, payload []byte)) {
	d.mu.Lock()
	nt := d.trust
	d.mu.Unlock()
	if nt == nil {
		return
	}
	nt.mu.Lock()
	nt.OtherSession, nt.OtherNode = session, machine
	nt.mu.Unlock()
}

func (nt *nodeTrust) Name() string { return "relay" }

// rosterChanged reacts to a new roster: a kicked machine wipes its keys.
func (nt *nodeTrust) rosterChanged() {
	r := nt.c.Roster()
	if r == nil {
		return
	}
	if _, ok := r.Node(nt.d.conf().NodeID); !ok {
		slog.Error("daemon: this machine was removed from the roster; deleting its keys")
		os.Remove(filepath.Join(trustDir(nt.d.root), "keys.json"))
		nt.mu.Lock()
		nt.removed = true
		nt.mu.Unlock()
		nt.r.CloseAll(errors.New("removed from the roster"))
	}
}

// brain waits for a live lease and returns its holder.
func (nt *nodeTrust) brain(ctx context.Context) (string, int64, error) {
	select {
	case <-nt.c.Ready():
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
	for {
		if holder, term, ok := nt.c.Brain(); ok {
			return holder, term, nil
		}
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-nt.leases:
		case <-time.After(3 * time.Second):
		}
	}
}

type linkConn struct {
	nt     *nodeTrust
	l      *tunnel.Link
	holder string
	term   int64
	stop   chan struct{}
	once   sync.Once
}

func (nt *nodeTrust) Dial(ctx context.Context) (FrameConn, error) {
	nt.mu.Lock()
	removed := nt.removed
	nt.mu.Unlock()
	if removed {
		return nil, ErrRevoked
	}
	holder, term, err := nt.brain(ctx)
	if err != nil {
		return nil, err
	}
	l, err := nt.r.Dial(ctx, trust.NodeAddr(holder))
	if err != nil {
		return nil, err
	}
	lc := &linkConn{nt: nt, l: l, holder: holder, term: term, stop: make(chan struct{})}
	go lc.watch()
	return lc, nil
}

// watch closes the link when the lease moves on: frames from a brain that
// no longer holds it are never read.
func (lc *linkConn) watch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-lc.stop:
			return
		case <-lc.l.Done():
			return
		case <-t.C:
		case <-lc.nt.leases:
			select {
			case lc.nt.leases <- struct{}{}: // let a waiting Dial see it too
			default:
			}
		}
		holder, term, ok := lc.nt.c.Brain()
		if !ok || holder != lc.holder || term != lc.term {
			slog.Info("daemon: the lease moved; reconnecting", "from", lc.holder, "to", holder)
			lc.Close()
			return
		}
	}
}

func (lc *linkConn) Read(ctx context.Context) (proto.Frame, error) {
	var f proto.Frame
	raw, err := lc.l.Read(ctx)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(raw, &f)
}

func (lc *linkConn) Write(ctx context.Context, f proto.Frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return lc.l.Write(ctx, raw)
}

func (lc *linkConn) Close() {
	lc.once.Do(func() { close(lc.stop) })
	lc.l.Close()
}

func (nt *nodeTrust) Call(ctx context.Context, method, path string, body, out any) error {
	holder, _, err := nt.brain(ctx)
	if err != nil {
		return err
	}
	return nt.rpc.JSON(ctx, trust.NodeAddr(holder), method, path, body, out)
}

func (nt *nodeTrust) Stream(ctx context.Context, path string, fn func([]byte) error) error {
	holder, _, err := nt.brain(ctx)
	if err != nil {
		return err
	}
	return nt.rpc.Stream(ctx, trust.NodeAddr(holder), path, fn)
}

// other handles everything that is not a link: RPC answers, the user's
// sessions (permits, terminal input, audit), and messages for the brain.
func (nt *nodeTrust) other(from string, payload []byte, gap bool) {
	if nt.rpc.Handle(from, payload) {
		return
	}
	nt.mu.Lock()
	sess, mach := nt.OtherSession, nt.OtherNode
	nt.mu.Unlock()
	var m sessionMsg
	json.Unmarshal(payload, &m)
	switch {
	case len(from) > 5 && from[:5] == "sess:":
		switch m.K {
		case "permit", "job.input", "job.resize", "audit.get", "node.ping":
			nt.session(from, m)
			return
		}
		if sess != nil {
			sess(from, payload)
		}
	case len(from) > 5 && from[:5] == "node:":
		if mach != nil {
			mach(from, payload)
		}
	}
}

// sessionMsg is what a page sends straight to a machine.
type sessionMsg struct {
	K      string           `json:"k"`
	ID     string           `json:"id,omitempty"`
	Permit json.RawMessage  `json:"permit,omitempty"` // payload bytes of a Permit, base64url
	Raw    string           `json:"raw,omitempty"`
	Assert *trust.Assertion `json:"assertion,omitempty"`
	JobID  string           `json:"job_id,omitempty"`
	Data   string           `json:"data,omitempty"`
	Cols   int              `json:"cols,omitempty"`
	Rows   int              `json:"rows,omitempty"`
	Since  int64            `json:"since,omitempty"`
}

func (nt *nodeTrust) reply(to string, v any) {
	raw, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := nt.c.Send(ctx, to, raw); err != nil {
		slog.Warn("daemon: cannot answer a session", "to", to, "err", err)
	}
}

func (nt *nodeTrust) session(from string, m sessionMsg) {
	sid := from[5:]
	switch m.K {
	case "node.ping":
		nt.reply(from, map[string]any{"k": "node.pong", "id": m.ID, "node": nt.d.conf().NodeID, "frozen": nt.isFrozen() != nil,
			"version": versionString(), "os": runtime.GOOS})
	case "permit":
		err := nt.addPermit(sid, m)
		res := map[string]any{"k": "permit.res", "id": m.ID, "ok": err == nil}
		if err != nil {
			res["error"] = err.Error()
		}
		nt.audit.add(from, "permit", err == nil, errString(err), []byte(m.Raw))
		nt.reply(from, res)
	case "job.input", "job.resize":
		nt.mu.Lock()
		owner := nt.jobOwners[m.JobID]
		nt.mu.Unlock()
		var err error
		if owner != sid {
			err = errors.New("这个终端不是这个会话打开的")
		} else if j := nt.d.admin.job(m.JobID); j == nil {
			err = errors.New("这个作业已经不在了")
		} else if m.K == "job.input" {
			err = j.input(m.Data)
		} else {
			err = j.resize(m.Cols, m.Rows)
		}
		if err != nil || m.K == "job.resize" {
			nt.audit.add(from, m.K, err == nil, errString(err), nil)
		}
		res := map[string]any{"k": m.K + ".res", "id": m.ID, "ok": err == nil}
		if err != nil {
			res["error"] = err.Error()
		}
		nt.reply(from, res)
	case "audit.get":
		nt.reply(from, map[string]any{"k": "audit", "id": m.ID, "node": nt.d.conf().NodeID, "entries": nt.audit.since(m.Since, 500), "head": nt.audit.head()})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

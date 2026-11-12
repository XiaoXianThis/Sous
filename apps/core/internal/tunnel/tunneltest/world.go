// Package tunneltest starts a relay with a user identity for tests and acts
// as the user's page. Only _test files import it.
package tunneltest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/relay"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust/trusttest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
)

const (
	RPID   = "sous.test"
	Origin = "https://sous.test"
)

// World is a relay, the user's identity and passkey, and the roster.
type World struct {
	T      testing.TB
	Dir    string // the relay's data directory
	Relay  *relay.Server
	HTTP   *httptest.Server
	URL    string
	ID     *trust.SignKey
	IDKEM  *trust.KEMKey
	Authn  *trusttest.Authenticator
	mu     sync.Mutex
	Roster trust.Roster
	doc    trust.Signed
	sess   *tunnel.Client
	scopeV int64
	pauseV int64
	domV   int64
}

// NewWorld starts a relay in a temp dir and claims it.
func NewWorld(t testing.TB) *World {
	t.Helper()
	dir := t.TempDir()
	srv, err := relay.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { hs.Close(); srv.Close() })
	w := &World{T: t, Dir: dir, Relay: srv, HTTP: hs, URL: hs.URL, Authn: trusttest.NewAuthenticator(RPID, Origin)}
	w.ID, _ = trust.NewSignKey()
	w.IDKEM, _ = trust.NewKEMKey()
	w.Roster = trust.Roster{T: trust.TRoster, V: 1, IAT: time.Now().Unix(), RPID: RPID, Origins: []string{Origin}, Relay: hs.URL,
		User: trust.User{SignPub: trust.B64.EncodeToString(w.ID.Public()), KEMPub: trust.B64.EncodeToString(w.IDKEM.Public()),
			Passkeys: []trust.Passkey{w.Authn.Passkey()}}}
	w.doc, _ = trust.Sign(w.ID, trust.TRoster, w.Roster)
	tok, err := srv.Init()
	if err != nil {
		t.Fatal(err)
	}
	bundle, _ := json.Marshal(map[string]string{"t": "identity", "sign_pub": w.Roster.User.SignPub})
	if err := tunnel.Claim(context.Background(), hs.URL, tok, trust.RosterUpdate{Doc: w.doc}, bundle); err != nil {
		t.Fatal(err)
	}
	return w
}

// Keys of a machine.
type Keys struct {
	ID   string
	Sign *trust.SignKey
	KEM  *trust.KEMKey
}

// NewKeys makes a machine's keys.
func NewKeys() Keys {
	s, _ := trust.NewSignKey()
	k, _ := trust.NewKEMKey()
	return Keys{ID: trust.NodeIDOf(s.Public()), Sign: s, KEM: k}
}

// Update changes the roster with fn and publishes it through the user's
// session, with the user's assertion when the change needs one.
func (w *World) Update(fn func(r *trust.Roster)) {
	w.T.Helper()
	w.mu.Lock()
	cur := w.Roster
	next := cur
	next.Nodes = append([]trust.RosterNode(nil), cur.Nodes...)
	fn(&next)
	next.V, next.Prev, next.IAT = cur.V+1, w.doc.Hash(), time.Now().Unix()
	doc, _ := trust.Sign(w.ID, trust.TRoster, next)
	u := trust.RosterUpdate{Doc: doc}
	if trust.RosterNeedsAssertion(&cur, &next) {
		raw, _ := doc.Payload()
		a := w.Authn.Assert(trust.CriticalChallenge(raw), true)
		u.Assertion = &a
	}
	w.Roster, w.doc = next, doc
	w.mu.Unlock()
	if err := w.Session().Put(context.Background(), "roster", u); err != nil {
		w.T.Fatal("roster:", err)
	}
}

// AddNode adds a machine (brain >= 0 makes it a brain with that priority).
func (w *World) AddNode(name string, brain int) Keys {
	k := NewKeys()
	w.Update(func(r *trust.Roster) {
		n := trust.RosterNode{ID: k.ID, Name: name, SignPub: trust.B64.EncodeToString(k.Sign.Public()),
			KEMPub: trust.B64.EncodeToString(k.KEM.Public()), KeyStorage: "software", Added: time.Now().Unix()}
		if brain >= 0 {
			n.Brain = &trust.Brain{Priority: brain}
		}
		r.Nodes = append(r.Nodes, n)
	})
	return k
}

// RosterDoc is the newest signed roster.
func (w *World) RosterDoc() trust.Signed {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.doc
}

// NewSession makes a session certificate and a connected client for it.
func (w *World) NewSession(handler func(from string, payload []byte, gap bool)) (*tunnel.Client, *trust.SignKey, *trust.KEMKey) {
	s, _ := trust.NewSignKey()
	k, _ := trust.NewKEMKey()
	now := time.Now()
	cert, _ := trust.Sign(w.ID, trust.TSession, trust.SessionCert{T: trust.TSession, SID: trust.KeyID(s.Public()),
		SignPub: trust.B64.EncodeToString(s.Public()), KEMPub: trust.B64.EncodeToString(k.Public()), IAT: now.Unix(), Exp: now.Add(time.Hour).Unix()})
	c, err := tunnel.New(tunnel.Config{URL: w.URL, Addr: trust.SessionAddr(trust.KeyID(s.Public())), Sign: s, KEM: k,
		Identity: w.ID.Public(), Cert: &cert, Handler: handler})
	if err != nil {
		w.T.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.T.Cleanup(cancel)
	go c.Run(ctx)
	waitReady(w.T, c)
	return c, s, k
}

// Session is the user's default session.
func (w *World) Session() *tunnel.Client {
	w.mu.Lock()
	s := w.sess
	w.mu.Unlock()
	if s != nil {
		return s
	}
	s, _, _ = w.NewSession(nil)
	w.mu.Lock()
	w.sess = s
	w.mu.Unlock()
	return s
}

// PutScopes signs and publishes the autonomy scope with an assertion.
func (w *World) PutScopes(sc trust.Scopes) {
	w.T.Helper()
	w.mu.Lock()
	w.scopeV++
	sc.T, sc.V, sc.IAT = trust.TScopes, w.scopeV, time.Now().Unix()
	w.mu.Unlock()
	doc, _ := trust.Sign(w.ID, trust.TScopes, sc)
	raw, _ := doc.Payload()
	a := w.Authn.Assert(trust.CriticalChallenge(raw), true)
	if err := w.Session().Put(context.Background(), "scopes", trust.ScopesUpdate{Doc: doc, Assertion: &a}); err != nil {
		w.T.Fatal("scopes:", err)
	}
}

// PutPause pauses or resumes all work.
func (w *World) PutPause(paused bool) {
	w.T.Helper()
	w.mu.Lock()
	w.pauseV++
	v := w.pauseV
	w.mu.Unlock()
	doc, _ := trust.Sign(w.ID, trust.TPause, trust.Pause{T: trust.TPause, V: v, IAT: time.Now().Unix(), Paused: paused})
	if err := w.Session().Put(context.Background(), "pause", doc); err != nil {
		w.T.Fatal("pause:", err)
	}
}

// PutDomain makes a new domain epoch wrapped to the user and every brain.
func (w *World) PutDomain() []byte {
	w.T.Helper()
	key := trust.RandomBytes(32)
	w.mu.Lock()
	w.domV++
	v := w.domV
	r := w.Roster
	w.mu.Unlock()
	wraps := map[string]string{}
	wraps["user"], _ = trust.WrapDomainKey(key, v, "user", w.IDKEM.Public())
	for _, n := range r.Brains() {
		pub, _ := trust.B64.DecodeString(n.KEMPub)
		wraps[n.ID], _ = trust.WrapDomainKey(key, v, n.ID, pub)
	}
	doc, _ := trust.Sign(w.ID, trust.TDomain, trust.DomainKeys{T: trust.TDomain, V: v, IAT: time.Now().Unix(),
		Epochs: []trust.DomainEpoch{{Epoch: v, Wraps: wraps}}})
	if err := w.Session().Put(context.Background(), "domain", doc); err != nil {
		w.T.Fatal("domain:", err)
	}
	return key
}

func waitReady(t testing.TB, c *tunnel.Client) {
	select {
	case <-c.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("tunnel client never became ready")
	}
}

// Client starts a machine's tunnel client.
func (w *World) Client(k Keys, cfg tunnel.Config) *tunnel.Client {
	cfg.URL, cfg.Addr, cfg.Sign, cfg.KEM, cfg.Identity = w.URL, trust.NodeAddr(k.ID), k.Sign, k.KEM, w.ID.Public()
	c, err := tunnel.New(cfg)
	if err != nil {
		w.T.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.T.Cleanup(cancel)
	go c.Run(ctx)
	waitReady(w.T, c)
	return c
}

// Eventually polls cond for up to 10 seconds.
func Eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for", what)
}

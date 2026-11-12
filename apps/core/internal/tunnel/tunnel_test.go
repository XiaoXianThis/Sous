package tunnel_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel/tunneltest"
)

type inbox struct {
	mu   sync.Mutex
	msgs []string
	from []string
	gaps int
}

func TestCanceledRequestsPreserveConnection(t *testing.T) {
	for _, operation := range []string{"get", "send"} {
		t.Run(operation, func(t *testing.T) {
			w := tunneltest.NewWorld(t)
			k := w.AddNode("mac", -1)
			var received inbox
			node := w.Client(k, tunnel.Config{Handler: received.handle})
			sess, _, _ := w.NewSession(nil)
			tunneltest.Eventually(t, "node roster", func() bool { return node.Roster() != nil })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			if operation == "get" {
				_, err = sess.Get(ctx, "roster")
			} else {
				err = sess.Send(ctx, trust.NodeAddr(k.ID), []byte("canceled"))
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request: %v", err)
			}
			if err := sess.Send(context.Background(), trust.NodeAddr(k.ID), []byte("still connected")); err != nil {
				t.Fatalf("canceled request broke the shared connection: %v", err)
			}
			tunneltest.Eventually(t, "next message delivered", func() bool { return received.count() == 1 })
		})
	}
}

func (b *inbox) handle(from string, p []byte, gap bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, string(p))
	b.from = append(b.from, from)
	if gap {
		b.gaps++
	}
}

func (b *inbox) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.msgs)
}

func TestSessionToNodeAndBack(t *testing.T) {
	w := tunneltest.NewWorld(t)
	k := w.AddNode("mac", -1)
	var nodeIn, sessIn inbox
	node := w.Client(k, tunnel.Config{Handler: nodeIn.handle, StateDir: t.TempDir()})
	sess, _, _ := w.NewSession(sessIn.handle)
	ctx := context.Background()
	tunneltest.Eventually(t, "node sees itself in the roster", func() bool { return node.Roster() != nil })

	if err := sess.Send(ctx, trust.NodeAddr(k.ID), []byte(`{"hello":1}`)); err != nil {
		t.Fatal(err)
	}
	tunneltest.Eventually(t, "node receives", func() bool { return nodeIn.count() == 1 })
	if nodeIn.from[0] != sess.Addr() {
		t.Fatal("wrong sender", nodeIn.from[0])
	}
	// The node answers the session it learned from the certificate.
	if err := node.Send(ctx, sess.Addr(), []byte(`{"ok":1}`)); err != nil {
		t.Fatal(err)
	}
	tunneltest.Eventually(t, "session receives", func() bool { return sessIn.count() == 1 })

	// A machine that is offline gets its mail when it comes back.
	for i := 0; i < 3; i++ {
		sess.Send(ctx, trust.NodeAddr(k.ID), []byte(`{"n":1}`))
	}
	tunneltest.Eventually(t, "queued mail delivered", func() bool { return nodeIn.count() == 4 })
}

func TestChannelsRekey(t *testing.T) {
	old := trust.ChanRekeyAfter
	trust.ChanRekeyAfter = 200 * time.Millisecond
	t.Cleanup(func() { trust.ChanRekeyAfter = old })
	w := tunneltest.NewWorld(t)
	k := w.AddNode("mac", -1)
	var nodeIn, sessIn inbox
	node := w.Client(k, tunnel.Config{Handler: nodeIn.handle, StateDir: t.TempDir()})
	sess, _, _ := w.NewSession(sessIn.handle)
	ctx := context.Background()
	tunneltest.Eventually(t, "node sees itself in the roster", func() bool { return node.Roster() != nil })
	for i := 1; i <= 3; i++ {
		if err := sess.Send(ctx, trust.NodeAddr(k.ID), []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
		tunneltest.Eventually(t, "node receives", func() bool { return nodeIn.count() == i })
		time.Sleep(300 * time.Millisecond)
	}
	if in, _ := node.Channels(); in < 2 {
		t.Fatalf("the session kept one channel (%d inbound): no rekey", in)
	}
	nodeIn.mu.Lock()
	gaps := nodeIn.gaps
	nodeIn.mu.Unlock()
	if gaps != 0 {
		t.Fatal("a rekey looked like lost messages")
	}
}

func TestMachineNotInRosterIsRefused(t *testing.T) {
	w := tunneltest.NewWorld(t)
	stranger := tunneltest.NewKeys()
	c, err := tunnel.New(tunnel.Config{URL: w.URL, Addr: trust.NodeAddr(stranger.ID), Sign: stranger.Sign, KEM: stranger.KEM, Identity: w.ID.Public()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go c.Run(ctx)
	select {
	case <-c.Ready():
		t.Fatal("a machine outside the roster connected")
	case <-ctx.Done():
	}
}

func TestImpostorKeyRefused(t *testing.T) {
	w := tunneltest.NewWorld(t)
	k := w.AddNode("mac", -1)
	other := tunneltest.NewKeys()
	// Claims the machine's address with another key.
	c, _ := tunnel.New(tunnel.Config{URL: w.URL, Addr: trust.NodeAddr(k.ID), Sign: other.Sign, KEM: other.KEM, Identity: w.ID.Public()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go c.Run(ctx)
	select {
	case <-c.Ready():
		t.Fatal("an impostor took a machine's address")
	case <-ctx.Done():
	}
}

func TestNodeCannotImpersonateUserOrOtherNode(t *testing.T) {
	w := tunneltest.NewWorld(t)
	a := w.AddNode("a", -1)
	b := w.AddNode("b", -1)
	var bIn inbox
	nb := w.Client(b, tunnel.Config{Handler: bIn.handle})
	na := w.Client(a, tunnel.Config{})
	tunneltest.Eventually(t, "rosters", func() bool { return na.Roster() != nil && na.Roster().V >= 3 })
	// A opens a channel to B but signs the opening as if it were the user's
	// session: it has no certificate, so B rejects it.
	fakeSess := trust.SessionAddr("fake")
	out, _ := trust.NewOutChannel(fakeSess, trust.NodeAddr(b.ID), a.Sign, b.KEM.Public(), time.Now())
	wire := out.Seal([]byte(`{"evil":1}`))
	raw, _ := json.Marshal(wire)
	rawSend(t, w, a, trust.NodeAddr(b.ID), raw)
	// And as machine B itself (wrong key for B).
	out2, _ := trust.NewOutChannel(trust.NodeAddr(b.ID), trust.NodeAddr(b.ID), a.Sign, b.KEM.Public(), time.Now())
	raw2, _ := json.Marshal(out2.Seal([]byte(`{"evil":2}`)))
	rawSend(t, w, a, trust.NodeAddr(b.ID), raw2)
	time.Sleep(300 * time.Millisecond)
	if bIn.count() != 0 {
		t.Fatal("B accepted a forged message:", bIn.msgs)
	}
	_ = nb
}

// rawSend sends arbitrary data as machine k through a fresh connection.
func rawSend(t *testing.T, w *tunneltest.World, k tunneltest.Keys, to string, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, tunnel.WSURL(w.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var ch struct{ Nonce string }
	_, raw, _ := ws.Read(ctx)
	json.Unmarshal(raw, &ch)
	nonce, _ := trust.B64.DecodeString(ch.Nonce)
	auth, _ := json.Marshal(map[string]string{"t": "auth", "addr": trust.NodeAddr(k.ID), "pub": trust.B64.EncodeToString(k.Sign.Public()),
		"sig": trust.B64.EncodeToString(k.Sign.Sign("sous/v1/relay-auth", nonce))})
	ws.Write(ctx, websocket.MessageText, auth)
	ws.Read(ctx)
	msg, _ := json.Marshal(map[string]any{"t": "send", "to": to, "data": json.RawMessage(data)})
	ws.Write(ctx, websocket.MessageText, msg)
	time.Sleep(100 * time.Millisecond)
}

func TestRosterRollbackAndKick(t *testing.T) {
	w := tunneltest.NewWorld(t)
	a := w.AddNode("a", -1)
	old := w.RosterDoc() // v2
	na := w.Client(a, tunnel.Config{StateDir: t.TempDir()})
	b := w.AddNode("b", -1) // v3
	tunneltest.Eventually(t, "a sees v3", func() bool { return na.Roster().V == 3 })
	// Kick b (v4).
	w.Update(func(r *trust.Roster) {
		r.Nodes = r.Nodes[:1]
	})
	tunneltest.Eventually(t, "a sees the kick", func() bool { return na.Roster().V == 4 })
	if _, ok := na.Roster().Node(b.ID); ok {
		t.Fatal("kicked machine still listed")
	}
	// The relay may not take the old roster back, and the machine would not
	// believe it either.
	if err := w.Session().Put(context.Background(), "roster", trust.RosterUpdate{Doc: old}); err == nil {
		t.Fatal("relay accepted a rollback")
	}
	// Kicked machine cannot connect any more.
	c, _ := tunnel.New(tunnel.Config{URL: w.URL, Addr: trust.NodeAddr(b.ID), Sign: b.Sign, KEM: b.KEM, Identity: w.ID.Public()})
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	go c.Run(ctx)
	select {
	case <-c.Ready():
		t.Fatal("kicked machine connected")
	case <-ctx.Done():
	}
}

func TestMissedRosterVersionsAreWalked(t *testing.T) {
	w := tunneltest.NewWorld(t)
	a := w.AddNode("a", -1) // v2
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	c1, _ := tunnel.New(tunnel.Config{URL: w.URL, Addr: trust.NodeAddr(a.ID), Sign: a.Sign, KEM: a.KEM, Identity: w.ID.Public(), StateDir: dir})
	go c1.Run(ctx)
	<-c1.Ready()
	cancel()
	time.Sleep(100 * time.Millisecond)
	// While a is away the user adds a passkey (v3) and a machine (v4).
	extra := tunneltest.NewKeys()
	w.Update(func(r *trust.Roster) {
		p := w.Authn.Passkey()
		p.CredID, p.Label = "second", "second"
		r.User.Passkeys = append(r.User.Passkeys, p)
	})
	w.Update(func(r *trust.Roster) {
		r.Nodes = append(r.Nodes, trust.RosterNode{ID: extra.ID, Name: "x", SignPub: trust.B64.EncodeToString(extra.Sign.Public()), KEMPub: trust.B64.EncodeToString(extra.KEM.Public())})
	})
	c2 := w.Client(a, tunnel.Config{StateDir: dir})
	tunneltest.Eventually(t, "caught up to v4", func() bool { return c2.Roster() != nil && c2.Roster().V == 4 })
}

func TestLinkRoundTripAndPeerRestart(t *testing.T) {
	w := tunneltest.NewWorld(t)
	brainK := w.AddNode("brain", 0)
	nodeK := w.AddNode("node", -1)
	var bcfg tunnel.Config
	br := tunnel.NewRouter(&bcfg)
	accepted := make(chan *tunnel.Link, 4)
	br.Accept = func(l *tunnel.Link) {
		accepted <- l
		for {
			f, err := l.Read(context.Background())
			if err != nil {
				return
			}
			l.Write(context.Background(), f) // echo
		}
	}
	brain := w.Client(brainK, bcfg)
	br.C = brain
	var ncfg tunnel.Config
	nr := tunnel.NewRouter(&ncfg)
	node := w.Client(nodeK, ncfg)
	nr.C = node
	ctx := context.Background()
	l, err := nr.Dial(ctx, trust.NodeAddr(brainK.ID))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := l.Write(ctx, json.RawMessage(`{"i":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if _, err := l.Read(rctx); err != nil {
			t.Fatal(i, err)
		}
		cancel()
	}
	// The brain closes the link: the node's side ends too.
	bl := <-accepted
	bl.Close()
	select {
	case <-l.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("node side did not see the close")
	}
	_ = brain
}

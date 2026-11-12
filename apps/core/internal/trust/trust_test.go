package trust_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust/trusttest"
)

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

type world struct {
	id    *trust.SignKey
	idKEM *trust.KEMKey
	authn *trusttest.Authenticator
	r     trust.Roster
	doc   trust.Signed
}

func newWorld(t *testing.T) *world {
	w := &world{id: must[*trust.SignKey](t)(trust.NewSignKey()), idKEM: must[*trust.KEMKey](t)(trust.NewKEMKey()),
		authn: trusttest.NewAuthenticator("sous.example", "https://sous.example")}
	w.r = trust.Roster{T: trust.TRoster, V: 1, IAT: time.Now().Unix(), RPID: "sous.example", Origins: []string{"https://sous.example"},
		Relay: "wss://relay.example", User: trust.User{SignPub: trust.B64.EncodeToString(w.id.Public()),
			KEMPub: trust.B64.EncodeToString(w.idKEM.Public()), Passkeys: []trust.Passkey{w.authn.Passkey()}}}
	w.doc = must[trust.Signed](t)(trust.Sign(w.id, trust.TRoster, w.r))
	return w
}

func TestSignVerify(t *testing.T) {
	k := must[*trust.SignKey](t)(trust.NewSignKey())
	sig := k.Sign("ctx", []byte("hello"))
	if err := trust.Verify(k.Public(), "ctx", []byte("hello"), sig); err != nil {
		t.Fatal(err)
	}
	if trust.Verify(k.Public(), "other", []byte("hello"), sig) == nil {
		t.Fatal("signature verified under another context")
	}
	if trust.Verify(k.Public(), "ctx", []byte("hellO"), sig) == nil {
		t.Fatal("signature verified for another message")
	}
	k2 := must[*trust.SignKey](t)(trust.SignKeyFromBytes(k.Bytes()))
	if !bytes.Equal(k2.Public(), k.Public()) {
		t.Fatal("round trip of the private key changed it")
	}
}

func TestSignedDocTypeBinding(t *testing.T) {
	w := newWorld(t)
	var r trust.Roster
	if _, err := w.doc.Open(w.id.Public(), trust.TRoster, &r); err != nil || r.V != 1 {
		t.Fatal(err)
	}
	// The same bytes cannot pass as another document type.
	if _, err := w.doc.Open(w.id.Public(), trust.TScopes, nil); err == nil {
		t.Fatal("roster opened as scopes")
	}
	other := must[*trust.SignKey](t)(trust.NewSignKey())
	if _, err := w.doc.Open(other.Public(), trust.TRoster, nil); err == nil {
		t.Fatal("opened with the wrong key")
	}
	tampered := w.doc
	tampered.P = trust.B64.EncodeToString([]byte(`{"t":"roster","v":99}`))
	if _, err := tampered.Open(w.id.Public(), trust.TRoster, nil); err == nil {
		t.Fatal("tampered payload verified")
	}
}

func TestRosterUpdates(t *testing.T) {
	w := newWorld(t)
	now := time.Now()
	cur := must[trust.Roster](t)(trust.AcceptRoster(w.id.Public(), nil, trust.RosterUpdate{Doc: w.doc}, now, nil))
	node := must[*trust.SignKey](t)(trust.NewSignKey())
	nkem := must[*trust.KEMKey](t)(trust.NewKEMKey())
	next := cur
	next.V, next.Prev, next.IAT = 2, w.doc.Hash(), now.Unix()
	next.Nodes = []trust.RosterNode{{ID: trust.NodeIDOf(node.Public()), Name: "mac", SignPub: trust.B64.EncodeToString(node.Public()), KEMPub: trust.B64.EncodeToString(nkem.Public())}}
	doc := must[trust.Signed](t)(trust.Sign(w.id, trust.TRoster, next))
	raw, _ := doc.Payload()

	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc}, now, nil); err == nil {
		t.Fatal("adding a machine without an assertion was accepted")
	}
	noUV := w.authn.Assert(trust.CriticalChallenge(raw), false)
	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &noUV}, now, nil); err == nil {
		t.Fatal("an assertion without user verification was accepted")
	}
	wrongOrigin := w.authn.AssertFrom("https://evil.example", "sous.example", trust.CriticalChallenge(raw), true)
	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &wrongOrigin}, now, nil); err == nil {
		t.Fatal("an assertion from another origin was accepted")
	}
	wrongRP := w.authn.AssertFrom("https://sous.example", "evil.example", trust.CriticalChallenge(raw), true)
	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &wrongRP}, now, nil); err == nil {
		t.Fatal("an assertion for another rp id was accepted")
	}
	otherOp := w.authn.Assert(trust.CriticalChallenge([]byte("something else")), true)
	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &otherOp}, now, nil); err == nil {
		t.Fatal("an assertion over another operation was accepted")
	}
	stranger := trusttest.NewAuthenticator("sous.example", "https://sous.example")
	strangers := stranger.Assert(trust.CriticalChallenge(raw), true)
	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &strangers}, now, nil); err == nil {
		t.Fatal("an unknown passkey was accepted")
	}
	seen := map[string]bool{}
	used := func(c string) bool { old := seen[c]; seen[c] = true; return old }
	good := w.authn.Assert(trust.CriticalChallenge(raw), true)
	got, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &good}, now, used)
	if err != nil || len(got.Nodes) != 1 {
		t.Fatal(err)
	}
	if _, err := trust.AcceptRoster(w.id.Public(), &cur, trust.RosterUpdate{Doc: doc, Assertion: &good}, now, used); err == nil {
		t.Fatal("a replayed assertion was accepted")
	}
	// Rollback: an older version never replaces a newer one.
	if _, err := trust.AcceptRoster(w.id.Public(), &got, trust.RosterUpdate{Doc: w.doc}, now, nil); err == nil {
		t.Fatal("rolled back to v1")
	}
	// Freezing needs no assertion; unfreezing does.
	frozen := got
	frozen.V = 3
	frozen.Nodes = []trust.RosterNode{got.Nodes[0]}
	frozen.Nodes[0].Frozen = true
	fdoc := must[trust.Signed](t)(trust.Sign(w.id, trust.TRoster, frozen))
	f := must[trust.Roster](t)(trust.AcceptRoster(w.id.Public(), &got, trust.RosterUpdate{Doc: fdoc}, now, nil))
	thawed := f
	thawed.V = 4
	thawed.Nodes = []trust.RosterNode{f.Nodes[0]}
	thawed.Nodes[0].Frozen = false
	tdoc := must[trust.Signed](t)(trust.Sign(w.id, trust.TRoster, thawed))
	if _, err := trust.AcceptRoster(w.id.Public(), &f, trust.RosterUpdate{Doc: tdoc}, now, nil); err == nil {
		t.Fatal("unfreezing without an assertion was accepted")
	}
	// Opening a machine's console needs an assertion; closing it does not.
	a, b := got, got
	a.Nodes = []trust.RosterNode{got.Nodes[0]}
	b.Nodes = []trust.RosterNode{got.Nodes[0]}
	b.Nodes[0].Console = true
	if !trust.RosterNeedsAssertion(&a, &b) {
		t.Fatal("opening a console needs no assertion")
	}
	if trust.RosterNeedsAssertion(&b, &a) {
		t.Fatal("closing a console needs an assertion")
	}
	// A roster signed by anyone but the identity is rejected.
	evil := must[*trust.SignKey](t)(trust.NewSignKey())
	edoc := must[trust.Signed](t)(trust.Sign(evil, trust.TRoster, frozen))
	if _, err := trust.AcceptRoster(w.id.Public(), &got, trust.RosterUpdate{Doc: edoc}, now, nil); err == nil {
		t.Fatal("a roster signed by another key was accepted")
	}
}

func TestSessionCert(t *testing.T) {
	w := newWorld(t)
	sk := must[*trust.SignKey](t)(trust.NewSignKey())
	kk := must[*trust.KEMKey](t)(trust.NewKEMKey())
	now := time.Now()
	c := trust.SessionCert{T: trust.TSession, SID: trust.KeyID(sk.Public()), SignPub: trust.B64.EncodeToString(sk.Public()),
		KEMPub: trust.B64.EncodeToString(kk.Public()), IAT: now.Unix(), Exp: now.Add(15 * time.Minute).Unix()}
	doc := must[trust.Signed](t)(trust.Sign(w.id, trust.TSession, c))
	if _, err := trust.OpenSessionCert(&w.r, doc, now); err != nil {
		t.Fatal(err)
	}
	if _, err := trust.OpenSessionCert(&w.r, doc, now.Add(time.Hour)); err == nil {
		t.Fatal("expired certificate accepted")
	}
	c.SID = "someone-else"
	bad := must[trust.Signed](t)(trust.Sign(w.id, trust.TSession, c))
	if _, err := trust.OpenSessionCert(&w.r, bad, now); err == nil {
		t.Fatal("certificate with a mismatched id accepted")
	}
}

func TestChannel(t *testing.T) {
	sender := must[*trust.SignKey](t)(trust.NewSignKey())
	rk := must[*trust.KEMKey](t)(trust.NewKEMKey())
	now := time.Now()
	out := must[*trust.OutChannel](t)(trust.NewOutChannel("sess:a", "node:b", sender, rk.Public(), now))
	w1, w2, w3 := out.Seal([]byte("one")), out.Seal([]byte("two")), out.Seal([]byte("three"))
	if w1.Open == nil || w2.Open != nil {
		t.Fatal("only the first message carries the opening")
	}
	if _, err := trust.AcceptChannel(*w1.Open, sender.Public(), "node:c", rk, now); err == nil {
		t.Fatal("accepted a channel addressed to someone else")
	}
	other := must[*trust.SignKey](t)(trust.NewSignKey())
	if _, err := trust.AcceptChannel(*w1.Open, other.Public(), "node:b", rk, now); err == nil {
		t.Fatal("accepted an opening signed by someone else")
	}
	if _, err := trust.AcceptChannel(*w1.Open, sender.Public(), "node:b", rk, now.Add(time.Hour)); err == nil {
		t.Fatal("accepted a stale opening")
	}
	wrongKey := must[*trust.KEMKey](t)(trust.NewKEMKey())
	if _, err := trust.AcceptChannel(*w1.Open, sender.Public(), "node:b", wrongKey, now); err == nil {
		t.Fatal("accepted with the wrong KEM key")
	}
	in := must[*trust.InChannel](t)(trust.AcceptChannel(*w1.Open, sender.Public(), "node:b", rk, now))
	if pt, err := in.Open(w1); err != nil || string(pt) != "one" {
		t.Fatal(pt, err)
	}
	if _, err := in.Open(w1); err == nil {
		t.Fatal("replay accepted")
	}
	tampered := w2
	tampered.CT = tampered.CT[:len(tampered.CT)-2] + "AA"
	if _, err := in.Open(tampered); err == nil {
		t.Fatal("tampered message accepted")
	}
	moved := w2
	moved.Seq = 5
	if _, err := in.Open(moved); err == nil {
		t.Fatal("message accepted under another sequence number")
	}
	if pt, err := in.Open(w3); !errors.Is(err, trust.ErrGap) || string(pt) != "three" {
		t.Fatal("gap not reported", err)
	}
	if _, err := in.Open(w2); err == nil {
		t.Fatal("older message accepted after a newer one")
	}
}

func TestSealToAndDomain(t *testing.T) {
	k := must[*trust.KEMKey](t)(trust.NewKEMKey())
	k2 := must[*trust.KEMKey](t)(trust.KEMKeyFromBytes(k.Bytes()))
	if !bytes.Equal(k.Public(), k2.Public()) {
		t.Fatal("KEM seed round trip changed the key")
	}
	key := trust.RandomBytes(32)
	w, err := trust.WrapDomainKey(key, 3, "nX", k.Public())
	if err != nil {
		t.Fatal(err)
	}
	d := trust.DomainKeys{T: trust.TDomain, Epochs: []trust.DomainEpoch{{Epoch: 3, Wraps: map[string]string{"nX": w}}}}
	got, err := d.Key(3, "nX", k)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatal(err)
	}
	// A wrap for one member does not open as another's.
	d.Epochs[0].Wraps["nY"] = w
	if _, err := d.Key(3, "nY", k); err == nil {
		t.Fatal("a wrap opened under another member's label")
	}
	ct, _ := trust.SymSeal(key, []byte("a"), []byte("data"))
	if _, err := trust.SymOpen(key, []byte("b"), ct); err == nil {
		t.Fatal("SymOpen ignored the aad")
	}
}

func TestPairCode(t *testing.T) {
	c := trust.PairCode{RelayHash: trust.RelayHash("wss://r.example/"), IdentityFP: trust.Fingerprint([]byte("id")),
		Secret: trust.RandomBytes(16), Exp: time.Unix(time.Now().Add(10*time.Minute).Unix(), 0)}
	s := c.Encode()
	if !strings.HasPrefix(s, "sous1-") || len(s) > 110 {
		t.Fatal(s)
	}
	got, err := trust.ParsePairCode("  " + s + "\n")
	if err != nil || !bytes.Equal(got.Secret, c.Secret) || !got.Exp.Equal(c.Exp) || !bytes.Equal(got.RelayHash, trust.RelayHash("wss://r.example")) {
		t.Fatal(err)
	}
	if _, err := trust.ParsePairCode(s[:len(s)-3]); err == nil {
		t.Fatal("truncated code parsed")
	}
	mac := trust.PairMAC(c.Secret, "node", []byte("x"))
	if !trust.CheckPairMAC(c.Secret, "node", []byte("x"), mac) || trust.CheckPairMAC(c.Secret, "app", []byte("x"), mac) {
		t.Fatal("pair MAC does not bind the side")
	}
}

func TestLease(t *testing.T) {
	w := newWorld(t)
	b := must[*trust.SignKey](t)(trust.NewSignKey())
	id := trust.NodeIDOf(b.Public())
	w.r.Nodes = []trust.RosterNode{{ID: id, SignPub: trust.B64.EncodeToString(b.Public()), Brain: &trust.Brain{Priority: 1}}}
	now := time.Now()
	l := trust.Lease{T: trust.TLease, Term: 2, Holder: id, IAT: now.Unix(), Exp: now.Add(trust.LeaseTTL).Unix()}
	doc := must[trust.Signed](t)(trust.Sign(b, trust.TLease, l))
	got, err := trust.OpenLease(&w.r, doc)
	if err != nil || !got.Live(now) || got.Live(now.Add(2*time.Minute)) {
		t.Fatal(err)
	}
	w.r.Nodes[0].Brain = nil
	if _, err := trust.OpenLease(&w.r, doc); err == nil {
		t.Fatal("a machine that is not a brain held the lease")
	}
	if !(trust.Lease{Term: 3, Holder: "x"}).Newer(got) || (trust.Lease{Term: 2, Holder: "y", Exp: got.Exp + 9}).Newer(got) {
		t.Fatal("lease ordering")
	}
}

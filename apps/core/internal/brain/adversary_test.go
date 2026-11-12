package brain_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel/tunneltest"
)

// The relay is the attacker here (requirements S1, S3, S8; design §12): it
// replays, redirects and forges. The machines must accept none of it.

type tap struct {
	mu   sync.Mutex
	msgs []struct {
		from, to string
		data     json.RawMessage
	}
}

func (t *tap) record(from, to string, data json.RawMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.msgs = append(t.msgs, struct {
		from, to string
		data     json.RawMessage
	}{from, to, append(json.RawMessage(nil), data...)})
}

func (t *tap) to(addr string) []struct {
	from, to string
	data     json.RawMessage
} {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []struct {
		from, to string
		data     json.RawMessage
	}
	for _, m := range t.msgs {
		if m.to == addr {
			out = append(out, m)
		}
	}
	return out
}

// accepted counts the run.start commands a machine accepted (its audit log).
func accepted(t *testing.T, root string) int {
	f, err := os.Open(filepath.Join(root, "trust", "audit.log"))
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e node.AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type == "run.start" && e.OK && e.From == "brain" {
			n++
		}
	}
	return n
}

func TestMaliciousRelay(t *testing.T) {
	w := tunneltest.NewWorld(t)
	var tp tap
	w.Relay.Tap = tp.record
	brainM := startMachine(t, w, "大脑", 0)
	w.PutDomain()
	worker := startMachine(t, w, "工作机", -1)
	p := newPage(t, w)
	tunneltest.Eventually(t, "the brain leads", brainM.b.Leading)
	workerAddr, brainAddr := trust.NodeAddr(worker.cfg.NodeID), trust.NodeAddr(brainM.cfg.NodeID)
	oldRoster := trust.RosterUpdate{Doc: w.RosterDoc()}

	if r := p.runUntilDone(worker.cfg.NodeID, "first"); r.Status != "succeeded" {
		t.Fatalf("baseline run: %+v", r)
	}
	base := accepted(t, worker.root)
	if base != 1 {
		t.Fatalf("expected one accepted run.start, got %d", base)
	}
	recorded := tp.to(workerAddr)

	// 1. Replay everything the brain ever sent the worker.
	for _, m := range recorded {
		w.Relay.Inject(m.to, m.from, m.data)
	}
	time.Sleep(800 * time.Millisecond)
	if n := accepted(t, worker.root); n != base {
		t.Fatalf("a replayed command was accepted (%d run.start)", n)
	}

	// 2. Restart the worker (its channels are gone) and replay again: the
	// openings were seen before and are refused.
	worker.stop()
	worker = runMachine(t, worker.root, worker.cfg)
	tunneltest.Eventually(t, "worker back", func() bool { return worker.d.Online() })
	for _, m := range recorded {
		w.Relay.Inject(m.to, m.from, m.data)
	}
	time.Sleep(800 * time.Millisecond)
	if n := accepted(t, worker.root); n != base {
		t.Fatalf("a replay after restart was accepted (%d run.start)", n)
	}

	// 3. Redirect the brain's ciphertext to another machine, and send
	// garbage claiming to be the brain: nothing opens.
	for _, m := range recorded {
		w.Relay.Inject(brainAddr, m.from, m.data)
	}
	w.Relay.Inject(workerAddr, brainAddr, json.RawMessage(`{"ch":"forged","seq":1,"ct":"AAAA"}`))

	// 4. A forged lease: the worker claims to be the brain, and a stranger too.
	wk, _, _ := node.LoadKeys(worker.root)
	forged, _ := trust.Sign(wk, trust.TLease, trust.Lease{T: trust.TLease, Term: 99, Holder: worker.cfg.NodeID, IAT: time.Now().Unix(), Exp: time.Now().Add(time.Hour).Unix()})
	raw, _ := json.Marshal(forged)
	w.Relay.InjectDoc("lease", raw)
	stranger := tunneltest.NewKeys()
	forged2, _ := trust.Sign(stranger.Sign, trust.TLease, trust.Lease{T: trust.TLease, Term: 100, Holder: stranger.ID, IAT: time.Now().Unix(), Exp: time.Now().Add(time.Hour).Unix()})
	raw2, _ := json.Marshal(forged2)
	w.Relay.InjectDoc("lease", raw2)

	// 5. Roll the roster back, and widen the scope without the user's
	// assertion (allowing open mode): both are ignored.
	rb, _ := json.Marshal(oldRoster)
	w.Relay.InjectDoc("roster", rb)
	wide, _ := trust.Sign(w.ID, trust.TScopes, trust.Scopes{T: trust.TScopes, V: 50, IAT: time.Now().Unix(), Default: trust.Scope{MaxMode: "auto", AllowOpen: true, AllowMerge: true, AllowOps: true}})
	sb, _ := json.Marshal(trust.ScopesUpdate{Doc: wide})
	w.Relay.InjectDoc("scopes", sb)
	time.Sleep(500 * time.Millisecond)

	// The worker still follows the real brain and the real scope.
	if r := p.runUntilDone(worker.cfg.NodeID, "after the attacks"); r.Status != "succeeded" {
		t.Fatalf("worker stopped working after the attacks: %+v", r)
	}
	if n := accepted(t, worker.root); n != base+1 {
		t.Fatalf("expected exactly one more accepted run.start, got %d", n-base)
	}
	raw, _ = os.ReadFile(filepath.Join(worker.root, "trust", "tunnel.json"))
	if strings.Contains(string(raw), `"v":50`) || strings.Contains(string(raw), "allow_open") {
		t.Fatal("the worker kept a scope without the user's assertion")
	}

	// 6. A real narrowing, then a replay of the older, wider scope.
	w.PutScopes(trust.Scopes{Default: trust.Scope{MaxMode: "auto", Nodes: []string{brainM.cfg.NodeID}}}) // v1
	before := []byte(nil)
	if b, err := w.Session().Get(t.Context(), "scopes"); err == nil {
		before = b
	}
	w.PutScopes(trust.Scopes{Default: trust.Scope{MaxMode: "auto", Nodes: []string{brainM.cfg.NodeID}}}) // v2, still narrow
	w.Relay.InjectDoc("scopes", before)                                                                  // replay v1
	tunneltest.Eventually(t, "narrow scope enforced", func() bool {
		r := p.runUntilDone(worker.cfg.NodeID, "should be refused")
		return r.Status == "failed" && strings.HasPrefix(r.Error, node.ScopeError)
	})
}

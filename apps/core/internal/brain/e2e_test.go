package brain_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/brain"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ipc"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel/tunneltest"
)

func TestMain(m *testing.M) {
	acptest.RunIfFake()
	trust.LeaseTTL, trust.LeaseRenew = 4*time.Second, time.Second
	os.Exit(m.Run())
}

type machine struct {
	root   string
	cfg    node.Config
	d      *node.Daemon
	b      *brain.Brain
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *machine) stop() {
	m.cancel()
	select {
	case <-m.done:
	case <-time.After(30 * time.Second):
	}
	select {
	case <-m.b.Done():
	case <-time.After(30 * time.Second):
	}
}

func startMachine(t *testing.T, w *tunneltest.World, name string, brainPriority int) *machine {
	t.Helper()
	root := t.TempDir()
	code, wait := w.PairPage(brainPriority)
	type res struct {
		id  string
		err error
	}
	ch := make(chan res, 1)
	go func() { id, err := wait(); ch <- res{id, err} }()
	cfg, err := node.PairRelay(context.Background(), root, w.URL, code, name, node.PairOptions{})
	if err != nil {
		t.Fatal("pair:", err)
	}
	if r := <-ch; r.err != nil || r.id != cfg.NodeID {
		t.Fatal("page side of pairing:", r.err, r.id, cfg.NodeID)
	}
	return runMachine(t, root, cfg)
}

func runMachine(t *testing.T, root string, cfg node.Config) *machine {
	cmd := acptest.Command("noop", "")
	spec := agents.Spec{Name: "fake", Title: "Fake", Binary: cmd.Path, Launch: append([]string{cmd.Path}, cmd.Args...), Env: cmd.Env, KeyEnv: []string{"FOO_KEY"}}
	d := node.NewDaemon(root, cfg, []agents.Spec{spec})
	ctx, cancel := context.WithCancel(context.Background())
	m := &machine{root: root, cfg: cfg, d: d, cancel: cancel, done: make(chan struct{})}
	b, err := brain.Attach(ctx, d, root, cfg.NodeID, func(b *brain.Brain) {
		b.Tick, b.SnapshotEvery = 200*time.Millisecond, 500*time.Millisecond
	})
	if err != nil {
		t.Fatal(err)
	}
	m.b = b
	go func() { d.Run(ctx); close(m.done) }()
	t.Cleanup(m.stop)
	return m
}

// page is the user's session talking to the brain over RPC.
type page struct {
	t   *testing.T
	c   *tunnel.Client
	rpc *tunnel.RPCClient
	sk  *trust.SignKey
	mu  sync.Mutex
	in  []map[string]any
}

func newPage(t *testing.T, w *tunneltest.World) *page {
	p := &page{t: t}
	p.c, p.sk, _ = w.NewSession(func(from string, payload []byte, gap bool) {
		if p.rpc != nil && p.rpc.Handle(from, payload) {
			return
		}
		var m map[string]any
		json.Unmarshal(payload, &m)
		p.mu.Lock()
		p.in = append(p.in, m)
		p.mu.Unlock()
	})
	p.rpc = tunnel.NewRPCClient(p.c)
	return p
}

func (p *page) brain() string {
	p.t.Helper()
	var holder string
	tunneltest.Eventually(p.t, "a brain holds the lease", func() bool {
		h, _, ok := p.c.Brain()
		holder = h
		return ok
	})
	return trust.NodeAddr(holder)
}

func (p *page) call(method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.rpc.JSON(ctx, p.brain(), method, path, body, out)
}

func (p *page) mustCall(method, path string, body, out any) {
	p.t.Helper()
	if err := p.call(method, path, body, out); err != nil {
		p.t.Fatal(method, path, err)
	}
}

// waitMsg waits for a direct message from a machine with kind k and id.
func (p *page) waitMsg(k, id string) map[string]any {
	var got map[string]any
	tunneltest.Eventually(p.t, "message "+k, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, m := range p.in {
			if m["k"] == k && m["id"] == id {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

// permit sends a critical permit straight to a machine, approved with the
// user's passkey, and waits for the machine's answer.
func (p *page) permit(w *tunneltest.World, nodeID string, action string, match map[string]string, critical bool) map[string]any {
	now := time.Now()
	pm := trust.Permit{T: trust.TPermit, ID: trust.B64.EncodeToString(trust.RandomBytes(8)), SID: trust.KeyID(p.sk.Public()), Node: nodeID,
		Action: action, Match: match, Critical: critical, IAT: now.Unix(), Exp: now.Add(5 * time.Minute).Unix()}
	raw, _ := json.Marshal(pm)
	msg := map[string]any{"k": "permit", "id": pm.ID, "raw": trust.B64.EncodeToString(raw)}
	if critical {
		a := w.Authn.Assert(trust.CriticalChallenge(raw), true)
		msg["assertion"] = a
	}
	data, _ := json.Marshal(msg)
	if err := p.c.Send(context.Background(), trust.NodeAddr(nodeID), data); err != nil {
		p.t.Fatal(err)
	}
	return p.waitMsg("permit.res", pm.ID)
}

type runView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (p *page) runUntilDone(nodeID, prompt string) runView {
	p.t.Helper()
	var r runView
	p.mustCall("POST", "/api/v1/runs", map[string]string{"node_id": nodeID, "agent": "fake", "prompt": prompt}, &r)
	tunneltest.Eventually(p.t, "run "+prompt+" ends", func() bool {
		var got struct {
			Run runView `json:"run"`
			runView
		}
		if p.call("GET", "/api/v1/runs/"+r.ID, nil, &got) != nil {
			return false
		}
		if got.Run.ID != "" {
			r = got.Run
		} else {
			r = got.runView
		}
		return r.Status == "succeeded" || r.Status == "failed" || r.Status == "interrupted"
	})
	p.t.Logf("run %q: %s %s", prompt, r.Status, r.Error)
	return r
}

func TestZeroTrustEndToEnd(t *testing.T) {
	w := tunneltest.NewWorld(t)
	brainM := startMachine(t, w, "大脑", 0)
	w.PutDomain()
	worker := startMachine(t, w, "工作机", -1)
	p := newPage(t, w)

	tunneltest.Eventually(t, "the brain machine leads", brainM.b.Leading)
	if worker.b.Leading() {
		t.Fatal("a machine that is not a brain leads")
	}
	var me struct {
		LoggedIn bool `json:"logged_in"`
	}
	p.mustCall("GET", "/api/v1/me", nil, &me)
	if !me.LoggedIn {
		t.Fatal("a verified session is not logged in")
	}
	tunneltest.Eventually(t, "both machines online at the brain", func() bool {
		var ns struct {
			Nodes []struct {
				ID     string `json:"id"`
				Online bool   `json:"online"`
			} `json:"nodes"`
		}
		if p.call("GET", "/api/v1/nodes", nil, &ns) != nil {
			return false
		}
		on := 0
		for _, n := range ns.Nodes {
			if n.Online && (n.ID == worker.cfg.NodeID || n.ID == brainM.cfg.NodeID) {
				on++
			}
		}
		return on == 2
	})

	// 1. Work inside the default scope runs.
	marker := "zero-trust-marker-" + trust.B64.EncodeToString(trust.RandomBytes(6))
	if r := p.runUntilDone(worker.cfg.NodeID, "hello "+marker); r.Status != "succeeded" {
		t.Fatalf("run in scope: %+v", r)
	}

	// 2. Out of scope: the worker refuses and says why.
	w.PutScopes(trust.Scopes{Default: trust.Scope{MaxMode: "auto", Nodes: []string{brainM.cfg.NodeID}}})
	tunneltest.Eventually(t, "worker sees the scope", func() bool {
		r := p.runUntilDone(worker.cfg.NodeID, "out of scope")
		return r.Status == "failed" && strings.HasPrefix(r.Error, node.ScopeError)
	})
	w.PutScopes(trust.Scopes{Default: trust.Scope{MaxMode: "auto"}})

	// 3. Critical admin operations need the user's own verification, checked
	// by the machine itself.
	env := map[string]string{"name": "FOO_KEY", "value": "secret"}
	err := p.call("PUT", "/api/v1/nodes/"+worker.cfg.NodeID+"/agents/fake/env", env, nil)
	if err == nil || !strings.Contains(err.Error(), "Face ID") {
		t.Fatal("set_env without a permit:", err)
	}
	envMatch := node.RequestMatch("agents.set_env", map[string]any{"agent": "fake", "name": "FOO_KEY", "value": "secret"})
	// A permit without the assertion is refused by the machine.
	if res := p.permit(w, worker.cfg.NodeID, trust.PermitAdmin, envMatch, false); res["ok"] == true {
		t.Fatal("a critical permit without an assertion was accepted")
	}
	// A permit that does not name the value approves nothing.
	p.permit(w, worker.cfg.NodeID, trust.PermitAdmin, map[string]string{"op": "agents.set_env", "agent": "fake", "name": "FOO_KEY"}, true)
	if err := p.call("PUT", "/api/v1/nodes/"+worker.cfg.NodeID+"/agents/fake/env", env, nil); err == nil {
		t.Fatal("a permit without the value's hash approved set_env")
	}
	// The brain cannot swap the approved value for another.
	if res := p.permit(w, worker.cfg.NodeID, trust.PermitAdmin, envMatch, true); res["ok"] != true {
		t.Fatal("permit refused:", res)
	}
	if err := p.call("PUT", "/api/v1/nodes/"+worker.cfg.NodeID+"/agents/fake/env", map[string]string{"name": "FOO_KEY", "value": "attacker"}, nil); err == nil {
		t.Fatal("a permit for one value let the brain write another")
	}
	// Three refusals froze the machine, as they should; the user thaws it.
	if res := p.permit(w, worker.cfg.NodeID, trust.PermitUnfreeze, nil, true); res["ok"] != true {
		t.Fatal("unfreeze refused:", res)
	}
	p.mustCall("PUT", "/api/v1/nodes/"+worker.cfg.NodeID+"/agents/fake/env", env, nil)
	// The permit was used up.
	if err := p.call("PUT", "/api/v1/nodes/"+worker.cfg.NodeID+"/agents/fake/env", env, nil); err == nil {
		t.Fatal("a used permit worked twice")
	}

	// 4. Brain-only critical requests need an assertion over the request.
	prov := map[string]any{"name": "x", "kind": "openai", "api_key": "sk-test", "base_url": "https://api.example.com/v1", "models": []string{"m"}}
	if err := p.call("POST", "/api/v1/models/providers", prov, nil); err == nil {
		t.Fatal("adding a model provider without Face ID worked")
	}

	// 4b. A machine's own terminal (sous chat) sees only what its console
	// allows: notes and its own approvals, until the user opens it.
	checkConsole(t, w, worker)

	// 5. Nothing readable reached the relay.
	time.Sleep(time.Second)
	assertNoPlaintext(t, w.Dir, marker)

	// 6. Kick the worker: it leaves the brain and loses its keys.
	w.Update(func(r *trust.Roster) {
		var keep []trust.RosterNode
		for _, n := range r.Nodes {
			if n.ID != worker.cfg.NodeID {
				keep = append(keep, n)
			}
		}
		r.Nodes = keep
	})
	tunneltest.Eventually(t, "the kicked machine deleted its keys", func() bool {
		_, err := os.Stat(filepath.Join(worker.root, "trust", "keys.json"))
		return os.IsNotExist(err)
	})
	tunneltest.Eventually(t, "the brain forgot the kicked machine", func() bool {
		var ns struct {
			Nodes []struct {
				ID      string `json:"id"`
				Revoked bool   `json:"revoked"`
			} `json:"nodes"`
		}
		if p.call("GET", "/api/v1/nodes", nil, &ns) != nil {
			return false
		}
		for _, n := range ns.Nodes {
			if n.ID == worker.cfg.NodeID && !n.Revoked {
				return false
			}
		}
		return true
	})

	// 7. Fail over: a second brain machine takes over with the state.
	standby := startMachine(t, w, "备用大脑", 1)
	w.PutDomain() // wrap the data key to the new brain too
	time.Sleep(time.Second)
	brainM.stop()
	tunneltest.Eventually(t, "the standby leads", standby.b.Leading)
	tunneltest.Eventually(t, "the standby has the runs", func() bool {
		var rs struct {
			Runs []struct {
				Prompt string `json:"prompt"`
			} `json:"runs"`
		}
		if p.call("GET", "/api/v1/runs", nil, &rs) != nil {
			return false
		}
		for _, r := range rs.Runs {
			if strings.Contains(r.Prompt, marker) {
				return true
			}
		}
		return false
	})
	assertNoPlaintext(t, w.Dir, marker)
}

func checkConsole(t *testing.T, w *tunneltest.World, m *machine) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.d.ServeIPC(ctx, nil); err != nil {
		t.Fatal(err)
	}
	cl, err := ipc.Dial(ipc.Path(m.root))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	call := func(method string, params any) error {
		cctx, ccancel := context.WithTimeout(ctx, 30*time.Second)
		defer ccancel()
		var out json.RawMessage
		return cl.Call(cctx, method, params, &out)
	}
	for _, c := range []struct {
		method string
		params any
	}{{"threads.list", nil}, {"messages.list", map[string]string{"thread_id": "x"}}, {"chat.send", map[string]string{"content": "hi"}}} {
		if err := call(c.method, c.params); err == nil || !strings.Contains(err.Error(), "终端对话") {
			t.Fatalf("%s on a closed console: %v", c.method, err)
		}
	}
	if err := call("note.add", map[string]string{"text": "a note from the worker"}); err != nil {
		t.Fatal("a note on a closed console:", err)
	}
	if err := call("approvals.list", nil); err != nil {
		t.Fatal("own approvals on a closed console:", err)
	}
	if err := call("approvals.decide", map[string]string{"id": "someone-elses"}); err == nil {
		t.Fatal("answered an approval of another machine")
	}
	w.Update(func(r *trust.Roster) {
		for i := range r.Nodes {
			if r.Nodes[i].ID == m.cfg.NodeID {
				r.Nodes[i].Console = true
			}
		}
	})
	tunneltest.Eventually(t, "the opened console reads the conversations", func() bool { return call("threads.list", nil) == nil })
}

func assertNoPlaintext(t *testing.T, dir, marker string) {
	t.Helper()
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		raw, _ := os.ReadFile(p)
		if bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("the relay stored plaintext in %s", p)
		}
		return nil
	})
}

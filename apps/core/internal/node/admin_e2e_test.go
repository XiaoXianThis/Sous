package node_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// api logs in to the Hub and returns a JSON caller.
func (e *env) api() func(method, path, body string, out any) int {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Post(e.srv.URL+"/api/v1/auth/dev-login", "", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	u, _ := url.Parse(e.srv.URL)
	csrf := ""
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "sous_csrf" {
			csrf = ck.Value
		}
	}
	return func(method, path, body string, out any) int {
		req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := c.Do(req)
		if err != nil {
			e.t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
}

// startAdminDaemon runs a daemon whose fake agent reports config options
// and has an installer, a login, an editable file and an API key variable.
func (e *env) startAdminDaemon(cfgFile string, change ...func(*agents.Spec)) (*node.Daemon, func()) {
	e.t.Helper()
	cmd := acptest.Command("write", "")
	spec := agents.Spec{
		Name: "fake", Title: "Fake", Binary: cmd.Path, Launch: append([]string{cmd.Path}, cmd.Args...),
		Env:     append(cmd.Env, "FAKE_ACP_OPTIONS=1", "FAKE_ACP_PROMPTS="+filepath.Join(e.root, "prompts.txt")),
		Install: map[string]agents.Recipe{"unix": {Script: `printf 'installing\n'; read answer; echo "got:$answer"`}},
		Login:   map[string]agents.Recipe{"unix": {Script: `echo "open https://example.com/device and enter ABCD-1234"; sleep 30`}},
		Configs: []proto.AgentConfigFile{{ID: "settings", Path: cfgFile, Format: "json"}, {ID: "toml", Path: cfgFile + ".toml", Format: "toml"}},
		KeyEnv:  []string{"FAKE_KEY"},
	}
	for _, c := range change {
		c(&spec)
	}
	d := node.NewDaemon(e.root, e.cfg, []agents.Spec{spec})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	e.waitFor("machine online with its inventory", func() bool {
		n, err := e.log.Store.Node(context.Background(), e.cfg.NodeID)
		return err == nil && e.hub.Online(n.ID) && strings.Contains(string(n.Agents), `"fake"`)
	})
	return d, func() { cancel(); <-done }
}

func (e *env) agentInfo() proto.AgentInfo {
	n, _ := e.log.Store.Node(context.Background(), e.cfg.NodeID)
	var as []proto.AgentInfo
	json.Unmarshal(n.Agents, &as)
	for _, a := range as {
		if a.Name == "fake" {
			return a
		}
	}
	return proto.AgentInfo{}
}

func TestAdminOptionsAndFiles(t *testing.T) {
	e := newEnv(t)
	cfgFile := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(cfgFile, []byte(`{"a": 1}`), 0o640)
	_, stop := e.startAdminDaemon(cfgFile)
	defer stop()
	call := e.api()
	base := "/api/v1/nodes/" + e.cfg.NodeID

	var nodes struct{ Nodes []struct{ Admin string } }
	call("GET", "/api/v1/nodes", "", &nodes)
	if len(nodes.Nodes) != 1 || nodes.Nodes[0].Admin != "on" {
		t.Fatalf("nodes = %+v", nodes)
	}

	// Probe: the options reach the inventory.
	var probe struct {
		Options []proto.ConfigOption
	}
	if code := call("POST", base+"/agents/fake/probe", "", &probe); code != 200 || len(probe.Options) != 2 {
		t.Fatalf("probe %d %+v", code, probe)
	}
	e.waitFor("options in the inventory", func() bool { return len(e.agentInfo().Options) == 2 })

	// Defaults are applied when a run's session opens.
	if code := call("PUT", base+"/agents/fake/defaults", `{"options":{"model":"smart","effort":"high","gone":"x"}}`, nil); code != 200 {
		t.Fatalf("defaults %d", code)
	}
	e.waitFor("defaults in the inventory", func() bool { return e.agentInfo().Defaults["model"] == "smart" })
	run, err := e.hub.Create(context.Background(), nodehub.CreateRun{ThreadID: e.main, NodeID: e.cfg.NodeID, Agent: "fake", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	a := e.pendingApproval(run.ID)
	e.hub.Decide(context.Background(), a.ID, "allow", "")
	e.waitFor("the run to end", func() bool { return e.run(run.ID).Status == event.RunStatusSucceeded })
	prompts, _ := os.ReadFile(filepath.Join(e.root, "prompts.txt"))
	p := string(prompts)
	if !strings.Contains(p, "set model=smart") || !strings.Contains(p, "set effort=high") || strings.Index(p, "set model") > strings.Index(p, "set effort") {
		t.Fatalf("agent saw: %s", p)
	}

	// API keys: only allowed names; names (not values) are reported.
	if code := call("PUT", base+"/agents/fake/env", `{"name":"PATH","value":"x"}`, nil); code != 400 {
		t.Fatalf("set PATH: %d", code)
	}
	if code := call("PUT", base+"/agents/fake/env", `{"name":"FAKE_KEY","value":"sekrit"}`, nil); code != 200 {
		t.Fatalf("set key: %d", code)
	}
	e.waitFor("the key name in the inventory", func() bool { return len(e.agentInfo().EnvSet) == 1 })
	n, _ := e.log.Store.Node(context.Background(), e.cfg.NodeID)
	if strings.Contains(string(n.Agents), "sekrit") {
		t.Fatal("the key value reached the Hub")
	}

	// Config files: read, conflict, invalid, write with a backup.
	var f proto.ConfigFile
	if code := call("GET", base+"/files/fake/settings", "", &f); code != 200 || f.Content != `{"a": 1}` || !f.Exists {
		t.Fatalf("read %d %+v", code, f)
	}
	body := func(content, sha string) string {
		raw, _ := json.Marshal(map[string]string{"content": content, "base_sha256": sha})
		return string(raw)
	}
	if code := call("PUT", base+"/files/fake/settings", body(`{"a": 2}`, "stale"), nil); code != 409 {
		t.Fatalf("stale write: %d", code)
	}
	if code := call("PUT", base+"/files/fake/settings", body(`{"a": `, f.SHA256), nil); code != 400 {
		t.Fatalf("invalid write: %d", code)
	}
	var w proto.ConfigFile
	if code := call("PUT", base+"/files/fake/settings", body(`{"a": 2}`, f.SHA256), &w); code != 200 || w.Content != `{"a": 2}` {
		t.Fatalf("write %d %+v", code, w)
	}
	if b, _ := os.ReadFile(cfgFile + ".sous-bak"); string(b) != `{"a": 1}` {
		t.Fatalf("backup = %q", b)
	}
	if fi, _ := os.Stat(cfgFile); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	// A new TOML file.
	if code := call("PUT", base+"/files/fake/toml", body("x = \n", ""), nil); code != 400 {
		t.Fatalf("bad toml: %d", code)
	}
	if code := call("PUT", base+"/files/fake/toml", body("model = \"o3\"\n", ""), nil); code != 200 {
		t.Fatalf("new toml: %d", code)
	}
	if code := call("GET", base+"/files/fake/nope", "", nil); code != 404 {
		t.Fatalf("unknown file: %d", code)
	}

	// node.json: the token is hidden and cannot be changed.
	var nf proto.ConfigFile
	call("GET", base+"/files/sous/node", "", &nf)
	if strings.Contains(nf.Content, e.cfg.Token) || strings.Contains(nf.Content, "sekrit") || !strings.Contains(nf.Content, `"***"`) {
		t.Fatalf("secret shown: %s", nf.Content)
	}
	var m map[string]any
	json.Unmarshal([]byte(nf.Content), &m)
	m["token"], m["concurrency"], m["remote_admin"] = "stolen", 3, false
	raw, _ := json.Marshal(m)
	if code := call("PUT", base+"/files/sous/node", body(string(raw), nf.SHA256), nil); code != 200 {
		t.Fatalf("write node.json: %d", code)
	}
	cfg, err := node.LoadConfig(e.root)
	if err != nil || cfg.Token != e.cfg.Token || cfg.Concurrency != 3 || !cfg.AdminEnabled() || cfg.AgentEnv["fake"]["FAKE_KEY"] != "sekrit" {
		t.Fatalf("node.json after write: %+v %v", cfg, err)
	}

	// From the Hub, safety settings only tighten.
	e.cfg, _ = node.LoadConfig(e.root)
	e.cfg.Role = "server"
	e.cfg.Policy.ExtraFloor = []string{"mine"}
	node.SaveConfig(e.root, e.cfg)
	call("GET", base+"/files/sous/node", "", &nf)
	m = map[string]any{}
	json.Unmarshal([]byte(nf.Content), &m)
	m["role"], m["remote_shell"] = "", false
	m["policy"] = map[string]any{"floor_disabled": true, "floor": []string{"x"}, "extra_floor": []string{"theirs"}}
	raw, _ = json.Marshal(m)
	if code := call("PUT", base+"/files/sous/node", body(string(raw), nf.SHA256), nil); code != 200 {
		t.Fatalf("write node.json: %d", code)
	}
	cfg, _ = node.LoadConfig(e.root)
	if cfg.Role != "server" || cfg.Policy.FloorDisabled || len(cfg.Policy.Floor) != 0 || !cfg.ShellEnabled() ||
		strings.Join(cfg.Policy.ExtraFloor, ",") != "theirs,mine" {
		t.Fatalf("remote edit loosened safety settings: %+v", cfg)
	}

	m["bogus"] = 1
	raw, _ = json.Marshal(m)
	call("GET", base+"/files/sous/node", "", &nf)
	if code := call("PUT", base+"/files/sous/node", body(string(raw), nf.SHA256), nil); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
}

func TestAdminJobs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake installer is a shell script")
	}
	e := newEnv(t)
	_, stop := e.startAdminDaemon(filepath.Join(t.TempDir(), "s.json"))
	defer stop()
	call := e.api()
	base := "/api/v1/nodes/" + e.cfg.NodeID

	live, unsub := e.log.Bus.Subscribe(256)
	defer unsub()
	var started proto.JobStarted
	if code := call("POST", base+"/agents/fake/install", `{"cols":80,"rows":24}`, &started); code != 200 || started.JobID == "" || started.Kind != "install" {
		t.Fatalf("install %d %+v", code, started)
	}
	var out strings.Builder
	read := func(until string) {
		deadline := time.After(10 * time.Second)
		for !strings.Contains(out.String(), until) {
			select {
			case ev := <-live:
				if ev.Type == event.AgentJobOutput {
					var p event.AgentJobPayload
					ev.Decode(&p)
					out.WriteString(p.Data)
				}
			case <-deadline:
				t.Fatalf("waiting for %q, got %q", until, out.String())
			}
		}
	}
	read("installing")
	if code := call("POST", base+"/agents/fake/install", "", nil); code != 409 {
		t.Fatalf("second install: %d", code)
	}
	if code := call("POST", base+"/jobs/"+started.JobID+"/input", `{"data":"yes\r"}`, nil); code != 200 {
		t.Fatalf("input %d", code)
	}
	read("got:yes")
	e.waitFor("the job's end", func() bool {
		evs, _ := e.log.Store.EventsSince(context.Background(), 0, 1000)
		for _, ev := range evs {
			if ev.Type == event.AgentJobEnded {
				return true
			}
		}
		return false
	})
	var tail proto.JobTail
	if code := call("GET", base+"/jobs/"+started.JobID, "", &tail); code != 200 || tail.Running || tail.Exit == nil || *tail.Exit != 0 || !strings.Contains(tail.Data, "got:yes") {
		t.Fatalf("tail %d %+v", code, tail)
	}

	// A login that waits is cancelled.
	var login proto.JobStarted
	call("POST", base+"/agents/fake/login", "", &login)
	read("ABCD-1234")
	call("POST", base+"/jobs/"+login.JobID+"/cancel", "", nil)
	e.waitFor("the login to stop", func() bool {
		var tl proto.JobTail
		call("GET", base+"/jobs/"+login.JobID, "", &tl)
		return !tl.Running
	})
}

func TestAdminDisabled(t *testing.T) {
	e := newEnv(t)
	off := false
	e.cfg.RemoteAdmin = &off
	node.SaveConfig(e.root, e.cfg)
	_, stop := e.startAdminDaemon(filepath.Join(t.TempDir(), "s.json"))
	defer stop()
	call := e.api()
	if code := call("GET", "/api/v1/nodes/"+e.cfg.NodeID+"/files/fake/settings", "", nil); code != 403 {
		t.Fatalf("read with admin off: %d", code)
	}
	var nodes struct{ Nodes []struct{ Admin string } }
	call("GET", "/api/v1/nodes", "", &nodes)
	if nodes.Nodes[0].Admin != "off" {
		t.Fatalf("admin = %q", nodes.Nodes[0].Admin)
	}
}

// Options sous sets for the permission mode (like Kiro's autopilot) win
// over the user's defaults and are not offered to the user.
func TestAdminForcedOptions(t *testing.T) {
	e := newEnv(t)
	_, stop := e.startAdminDaemon(filepath.Join(t.TempDir(), "s.json"), func(s *agents.Spec) {
		s.AskOptions = map[string]string{"effort": "low"}
		s.OpenOptions = map[string]string{"effort": "high"}
	})
	defer stop()
	call := e.api()
	base := "/api/v1/nodes/" + e.cfg.NodeID
	var probe struct{ Options []proto.ConfigOption }
	call("POST", base+"/agents/fake/probe", "", &probe)
	if len(probe.Options) != 1 || probe.Options[0].ID != "model" {
		t.Fatalf("offered options: %+v", probe.Options)
	}
	call("PUT", base+"/agents/fake/defaults", `{"options":{"effort":"medium","model":"smart"}}`, nil)
	e.waitFor("defaults", func() bool { return e.agentInfo().Defaults["model"] == "smart" })
	if _, ok := e.agentInfo().Defaults["effort"]; ok {
		t.Fatal("a forced option was accepted as a default")
	}
	run, err := e.hub.Create(context.Background(), nodehub.CreateRun{ThreadID: e.main, NodeID: e.cfg.NodeID, Agent: "fake", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	a := e.pendingApproval(run.ID)
	e.hub.Decide(context.Background(), a.ID, "allow", "")
	e.waitFor("the run to end", func() bool { return e.run(run.ID).Status == event.RunStatusSucceeded })
	prompts, _ := os.ReadFile(filepath.Join(e.root, "prompts.txt"))
	if p := string(prompts); !strings.Contains(p, "set effort=low") || strings.Contains(p, "effort=medium") || !strings.Contains(p, "set model=smart") {
		t.Fatalf("agent saw: %s", p)
	}
}

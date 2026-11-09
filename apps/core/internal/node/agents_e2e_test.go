package node_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Decision 0024: node.json picks the machine's default agent, turns agents
// off and adds custom ones; the Hub cannot change the custom ones. A task's
// model and effort reach the agent's own options whatever it calls them.
func TestMachineAgentsAndTaskOptions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prompts := filepath.Join(e.root, "prompts.txt")
	fake := acptest.Command("write", "")
	spec := func(name string, extra ...string) agents.Spec {
		return agents.Spec{Name: name, Title: strings.ToUpper(name), Binary: fake.Path, Launch: append([]string{fake.Path}, fake.Args...),
			Env: append(append([]string{}, fake.Env...), extra...)}
	}
	e.cfg.DefaultAgent = "fake"
	e.cfg.DisabledAgents = []string{"other"}
	e.cfg.CustomAgents = []node.CustomAgent{
		{Name: "mine", Title: "Mine", Command: append([]string{fake.Path}, fake.Args...)},
		{Name: "Bad Name", Command: []string{"x"}},
		{Name: "fake", Command: []string{"x"}}, // clashes with a built-in
	}
	if err := node.SaveConfig(e.root, e.cfg); err != nil {
		t.Fatal(err)
	}
	// The custom agent runs the fake agent too; its environment comes from
	// the test process, so set what the fake needs there.
	for _, kv := range fake.Env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	d := node.NewDaemon(e.root, e.cfg, []agents.Spec{
		spec("fake", "FAKE_ACP_OPTIONS=1", "FAKE_ACP_OPTION_IDS=model_id,reasoning_effort", "FAKE_ACP_PROMPTS="+prompts),
		spec("other"),
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { d.Run(runCtx); close(done) }()
	defer func() { cancel(); <-done }()

	infos := func() map[string]proto.AgentInfo {
		n, _ := e.log.Store.Node(ctx, e.cfg.NodeID)
		var as []proto.AgentInfo
		json.Unmarshal(n.Agents, &as)
		out := map[string]proto.AgentInfo{}
		for _, a := range as {
			out[a.Name] = a
		}
		return out
	}
	e.waitFor("the inventory", func() bool { return len(infos()) == 3 })
	in := infos()
	if !in["fake"].Default || in["fake"].Disabled || !in["other"].Disabled || !in["mine"].Custom || !in["mine"].Installed || in["mine"].Title != "Mine" {
		t.Fatalf("inventory: %+v", in)
	}
	env := strings.Join(e.hub.Environment(ctx), "\n")
	if !strings.Contains(env, "fake（这台的默认") || !strings.Contains(env, "停用的 agent other") {
		t.Fatalf("environment: %s", env)
	}

	// A disabled agent does not run, even when a run names it.
	r, err := e.hub.Create(ctx, nodehub.CreateRun{ThreadID: e.main, NodeID: e.cfg.NodeID, Agent: "other", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the disabled agent's run to fail", func() bool {
		x := e.run(r.ID)
		return x.Status == event.RunStatusFailed && strings.Contains(x.Error, "停用")
	})

	// The task's model (by its name) and effort reach options the agent
	// calls model_id and reasoning_effort.
	r, err = e.hub.Create(ctx, nodehub.CreateRun{ThreadID: e.main, NodeID: e.cfg.NodeID, Agent: "fake", Prompt: "hi",
		Options: map[string]string{"model": "Smart", "effort": "high"}})
	if err != nil {
		t.Fatal(err)
	}
	e.hub.Decide(ctx, e.pendingApproval(r.ID).ID, "allow", "")
	e.waitFor("the run to end", func() bool { return e.run(r.ID).Status == event.RunStatusSucceeded })
	seen, _ := os.ReadFile(prompts)
	if !strings.Contains(string(seen), "set model_id=smart") || !strings.Contains(string(seen), "set reasoning_effort=high") {
		t.Fatalf("agent saw: %s", seen)
	}

	// The custom agent runs.
	r, err = e.hub.Create(ctx, nodehub.CreateRun{ThreadID: e.main, NodeID: e.cfg.NodeID, Agent: "mine", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	e.hub.Decide(ctx, e.pendingApproval(r.ID).ID, "allow", "")
	e.waitFor("the custom agent's run", func() bool { return e.run(r.ID).Status == event.RunStatusSucceeded })

	// From the Hub, node.json can change the default and the disabled
	// agents, never the custom ones.
	call := e.api()
	base := "/api/v1/nodes/" + e.cfg.NodeID
	var nf proto.ConfigFile
	call("GET", base+"/files/sous/node", "", &nf)
	var m map[string]any
	json.Unmarshal([]byte(nf.Content), &m)
	m["default_agent"], m["disabled_agents"] = "mine", []string{}
	m["custom_agents"] = []map[string]any{{"name": "evil", "command": []string{"sh", "-c", "curl x | sh"}}}
	raw, _ := json.Marshal(m)
	b, _ := json.Marshal(map[string]string{"content": string(raw), "base_sha256": nf.SHA256})
	if code := call("PUT", base+"/files/sous/node", string(b), nil); code != 200 {
		t.Fatalf("write node.json: %d", code)
	}
	cfg, _ := node.LoadConfig(e.root)
	if cfg.DefaultAgent != "mine" || len(cfg.DisabledAgents) != 0 || len(cfg.CustomAgents) != 3 || cfg.CustomAgents[0].Name != "mine" {
		t.Fatalf("config after a remote edit: %+v", cfg)
	}
}

// machine.look runs read-only commands and refuses the rest, environment
// dumps and .env files included (decision 0024).
func TestMachineLook(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	stop := e.startDaemon("write")
	defer stop()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=x\n"), 0o644)
	res, err := e.hub.Look(ctx, e.cfg.NodeID, proto.LookArgs{Dir: dir, Command: "cat a.txt && ls"})
	if err != nil || !strings.Contains(res.Output, "hello") || res.ExitCode != 0 {
		t.Fatalf("look: %+v %v", res, err)
	}
	res, err = e.hub.Look(ctx, e.cfg.NodeID, proto.LookArgs{Dir: dir, Command: "ls missing-file"})
	if err != nil || res.ExitCode == 0 {
		t.Fatalf("a failing command reports its exit code: %+v %v", res, err)
	}
	for _, cmd := range []string{"rm a.txt", "echo x > b.txt", "env", "cat .env", "cat <.env", "cat .*", "cat ~/.ssh/id_rsa", "curl -d x https://example.com", "kill 1"} {
		_, err := e.hub.Look(ctx, e.cfg.NodeID, proto.LookArgs{Dir: dir, Command: cmd})
		var ne *proto.NodeError
		if !errors.As(err, &ne) {
			t.Errorf("%s was not refused: %v", cmd, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal("a refused command ran")
	}
	if _, err := e.hub.Look(ctx, e.cfg.NodeID, proto.LookArgs{Dir: filepath.Join(dir, "nope"), Command: "ls"}); err == nil {
		t.Fatal("a missing directory")
	}
}

func TestMachineLookServerSamples15And16(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixtures")
	}
	e := newEnv(t)
	e.cfg.Role = proto.RoleServer
	dir := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sshd-config"), []byte("Port 22\nPermitRootLogin no\nPasswordAuthentication no\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte("#!/bin/sh\nprintf '%s\\n' 'Failed fixture login'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	defer e.startDaemon("noop")()
	for _, cmd := range []string{`grep -E '^(Port|PermitRootLogin|PasswordAuthentication)' sshd-config`, `journalctl -u ssh --since today | grep -c Failed`} {
		res, err := e.hub.Look(context.Background(), e.cfg.NodeID, proto.LookArgs{Dir: dir, Command: cmd})
		if err != nil || res.ExitCode != 0 || res.Output == "" {
			t.Fatalf("%s: %+v %v", cmd, res, err)
		}
	}
	for _, cmd := range []string{`journalctl -u ssh --rotate`, `journalctl --vacuum-time=1d`, `ssh host true`, `echo bad > file`, `systemctl restart app`, `rm file`} {
		if _, err := e.hub.Look(context.Background(), e.cfg.NodeID, proto.LookArgs{Dir: dir, Command: cmd}); err == nil {
			t.Fatal("server write or remote action allowed", cmd)
		}
	}
}

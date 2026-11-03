package node_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestMain(m *testing.M) {
	acptest.RunIfFake()
	os.Exit(m.Run())
}

type env struct {
	t    *testing.T
	srv  *httptest.Server
	log  *eventlog.Log
	hub  *nodehub.Hub
	main string
	root string // the node's ~/.sous
	cfg  node.Config
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.New(st, event.NewBus())
	cfg := &config.Hub{PublicURL: "http://localhost:8787", Dev: true}
	a, _ := auth.New(st, cfg)
	hub, err := nodehub.New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpapi.New(cfg, log, a, hub.Routes))
	main, _ := log.EnsureMainThread(context.Background())
	e := &env{t: t, srv: srv, log: log, hub: hub, main: main, root: t.TempDir()}
	t.Cleanup(func() { srv.Close(); st.Close() })

	code, _ := hub.NewPairingCode()
	e.cfg, err = node.Pair(context.Background(), e.root, srv.URL, code, "测试机")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// startDaemon runs a daemon whose only agent is the fake in the given mode.
func (e *env) startDaemon(mode string) (stop func()) {
	e.t.Helper()
	cmd := acptest.Command(mode, "")
	spec := agents.Spec{Name: "fake", Title: "Fake", Binary: cmd.Path, Launch: append([]string{cmd.Path}, cmd.Args...), Env: cmd.Env}
	d := node.NewDaemon(e.root, e.cfg, []agents.Spec{spec})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	e.waitFor("machine online with its inventory", func() bool {
		n, err := e.log.Store.Node(context.Background(), e.cfg.NodeID)
		return err == nil && e.hub.Online(n.ID) && strings.Contains(string(n.Agents), `"fake"`)
	})
	return func() { cancel(); <-done }
}

func (e *env) waitFor(what string, ok func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s", what)
}

func (e *env) run(id string) store.Run {
	r, err := e.log.Store.Run(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) pendingApproval(runID string) store.Approval {
	e.t.Helper()
	var a store.Approval
	e.waitFor("an approval request", func() bool {
		as, _ := e.log.Store.Approvals(context.Background(), "pending", runID)
		if len(as) > 0 {
			a = as[0]
			return true
		}
		return false
	})
	return a
}

func (e *env) ended(runID string) store.Run {
	e.t.Helper()
	e.waitFor("the run to end", func() bool {
		s := e.run(runID).Status
		return s != event.RunStatusQueued && s != event.RunStatusRunning
	})
	return e.run(runID)
}

func gitRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "demo")
	os.MkdirAll(repo, 0o755)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		exec.Command("git", append([]string{"-C", repo}, args...)...).Run()
	}
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# demo\n"), 0o644)
	exec.Command("git", "-C", repo, "add", "-A").Run()
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "init").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	return repo
}

func TestRunWithApprovalEndToEnd(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("write")()
	repo := gitRepo(t)
	ctx := context.Background()

	run, err := e.hub.Create(ctx, nodehub.CreateRun{ThreadID: e.main, Agent: "fake", RepoPath: repo, Prompt: "创建 hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if run.NodeID != e.cfg.NodeID {
		t.Fatalf("picked node %s", run.NodeID)
	}
	a := e.pendingApproval(run.ID)
	if a.Title != "Write hello.txt" || len(a.Options) != 2 || a.ThreadID != e.main || !strings.Contains(a.Detail, "hello.txt") {
		t.Fatalf("approval: %+v", a)
	}
	if err := e.hub.Decide(ctx, a.ID, "allow", ""); err != nil {
		t.Fatal(err)
	}
	r := e.ended(run.ID)
	if r.Status != event.RunStatusSucceeded || !strings.Contains(r.Summary, "已经创建") || !strings.HasPrefix(r.Branch, "sous/") || !strings.Contains(r.DiffStat, "1 file changed") {
		t.Fatalf("run: %+v", r)
	}
	diff, _ := os.ReadFile(e.hub.DiffPath(run.ID))
	if !strings.Contains(string(diff), "hello.txt") || !strings.Contains(string(diff), "+hi") {
		t.Fatalf("diff: %s", diff)
	}
	if _, err := os.Stat(filepath.Join(repo, "hello.txt")); err == nil {
		t.Fatal("the user's checkout was modified")
	}
	logText, _ := os.ReadFile(e.hub.LogPath(run.ID))
	if !strings.Contains(string(logText), "我先看一下仓库") || !strings.Contains(string(logText), "Write hello.txt") {
		t.Fatalf("log: %s", logText)
	}
	// The stored events tell the story in order.
	evs, _ := e.log.Store.RunEvents(ctx, run.ID)
	var kinds []string
	for _, ev := range evs {
		kinds = append(kinds, string(ev.Type))
	}
	got := strings.Join(kinds, " ")
	for _, want := range []string{"run.queued", "run.started", "approval.requested", "approval.decided", "run.ended"} {
		if !strings.Contains(got, want) {
			t.Fatalf("events %s: missing %s", got, want)
		}
	}
	if a2, _ := e.log.Store.Approval(ctx, a.ID); a2.Status != "decided" || a2.OptionID != "allow" {
		t.Fatalf("approval after: %+v", a2)
	}
}

func TestRejectedApproval(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("write")()
	run, _ := e.hub.Create(context.Background(), nodehub.CreateRun{Agent: "fake", RepoPath: gitRepo(t), Prompt: "x"})
	a := e.pendingApproval(run.ID)
	e.hub.Decide(context.Background(), a.ID, "reject", "不要动")
	r := e.ended(run.ID)
	if !strings.Contains(r.Summary, "没有获得写入权限") || r.DiffStat != "" {
		t.Fatalf("run: %+v", r)
	}
	if err := e.hub.Decide(context.Background(), a.ID, "allow", ""); err == nil {
		t.Fatal("decided twice")
	}
}

func TestCancelRunningRun(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("slow")()
	run, _ := e.hub.Create(context.Background(), nodehub.CreateRun{Agent: "fake", Prompt: "慢慢来"}) // a general run
	e.waitFor("the run to start", func() bool { return e.run(run.ID).Status == event.RunStatusRunning })
	if err := e.hub.Cancel(context.Background(), run.ID, "测试取消"); err != nil {
		t.Fatal(err)
	}
	if r := e.ended(run.ID); r.Status != event.RunStatusCancelled || r.Branch != "" {
		t.Fatalf("run: %+v", r)
	}
}

func TestQueuedUntilMachineConnects(t *testing.T) {
	e := newEnv(t)
	run, err := e.hub.Create(context.Background(), nodehub.CreateRun{NodeID: e.cfg.NodeID, Agent: "fake", Prompt: "一般任务"})
	if err != nil {
		t.Fatal(err)
	}
	if e.run(run.ID).Status != event.RunStatusQueued {
		t.Fatal("run should wait for its machine")
	}
	// Without a node id, no online machine can be picked.
	if _, err := e.hub.Create(context.Background(), nodehub.CreateRun{Agent: "fake", Prompt: "x"}); err == nil {
		t.Fatal("picked an offline machine")
	}
	defer e.startDaemon("write")()
	e.waitFor("the queued run to start", func() bool { return e.run(run.ID).Status != event.RunStatusQueued })
	a := e.pendingApproval(run.ID)
	e.hub.Decide(context.Background(), a.ID, "allow", "")
	if r := e.ended(run.ID); r.Status != event.RunStatusSucceeded {
		t.Fatalf("run: %+v", r)
	}
}

func TestApprovalSurvivesReconnect(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("write")()
	run, _ := e.hub.Create(context.Background(), nodehub.CreateRun{Agent: "fake", RepoPath: gitRepo(t), Prompt: "x"})
	a := e.pendingApproval(run.ID)
	// Drop the connection while the agent waits for the answer.
	e.srv.CloseClientConnections()
	e.waitFor("the machine to reconnect", func() bool { return e.hub.Online(e.cfg.NodeID) })
	if err := e.hub.Decide(context.Background(), a.ID, "allow", ""); err != nil {
		t.Fatal(err)
	}
	if r := e.ended(run.ID); r.Status != event.RunStatusSucceeded {
		t.Fatalf("run: %+v", r)
	}
}

func TestDaemonRestartInterruptsRun(t *testing.T) {
	e := newEnv(t)
	stop := e.startDaemon("write")
	run, _ := e.hub.Create(context.Background(), nodehub.CreateRun{Agent: "fake", RepoPath: gitRepo(t), Prompt: "x"})
	e.pendingApproval(run.ID)
	stop() // the daemon goes away with the run in progress
	defer e.startDaemon("write")()
	r := e.ended(run.ID)
	if r.Status != event.RunStatusInterrupted || !strings.Contains(r.Error, "重启") {
		t.Fatalf("run: %+v", r)
	}
	if as, _ := e.log.Store.Approvals(context.Background(), "pending", run.ID); len(as) != 0 {
		t.Fatalf("approval still pending: %+v", as)
	}
}

func TestAgentNotLoggedIn(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("nologin")()
	run, _ := e.hub.Create(context.Background(), nodehub.CreateRun{Agent: "fake", Prompt: "x"})
	if r := e.ended(run.ID); r.Status != event.RunStatusFailed || !strings.Contains(r.Error, "还没有登录") {
		t.Fatalf("run: %+v", r)
	}
}

func TestRevokedMachineIsDisconnected(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("write")()
	if err := e.hub.Revoke(context.Background(), e.cfg.NodeID); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the machine to drop", func() bool { return !e.hub.Online(e.cfg.NodeID) })
	time.Sleep(1500 * time.Millisecond) // the daemon retries; the Hub must keep refusing it
	if e.hub.Online(e.cfg.NodeID) {
		t.Fatal("revoked machine reconnected")
	}
}

func TestPairingCodeIsSingleUse(t *testing.T) {
	e := newEnv(t)
	code, _ := e.hub.NewPairingCode()
	if _, err := node.Pair(context.Background(), t.TempDir(), e.srv.URL, code, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Pair(context.Background(), t.TempDir(), e.srv.URL, code, "b"); err == nil {
		t.Fatal("code used twice")
	}
	if _, err := node.Pair(context.Background(), t.TempDir(), e.srv.URL, "000000", "c"); err == nil {
		t.Fatal("made-up code accepted")
	}
}

func TestInventoryReportsAgents(t *testing.T) {
	e := newEnv(t)
	defer e.startDaemon("write")()
	n, _ := e.log.Store.Node(context.Background(), e.cfg.NodeID)
	var agents []proto.AgentInfo
	json.Unmarshal(n.Agents, &agents)
	if len(agents) != 1 || agents[0].Name != "fake" || !agents[0].Installed {
		t.Fatalf("agents: %+v", agents)
	}
	lines := strings.Join(e.hub.Environment(context.Background()), "\n")
	if !strings.Contains(lines, "测试机") || !strings.Contains(lines, "在线") || !strings.Contains(lines, "fake") {
		t.Fatalf("environment: %s", lines)
	}
}

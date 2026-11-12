package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

const (
	inventoryEvery = 10 * time.Minute
	maxBackoff     = 30 * time.Second
	welcomeTimeout = 15 * time.Second
)

// ErrRevoked means the Hub no longer accepts this machine's token.
var ErrRevoked = errors.New("这台机器已被踢出（或不再被接受），要再用请重新配对")

type Daemon struct {
	root string
	ob   *outbox
	sem  chan struct{}
	base []agents.Spec // the built-in agents, before node.json

	// cfgMu guards what node.json decides and a reload replaces.
	cfgMu  sync.RWMutex
	cfg    Config
	specs  []agents.Spec
	policy *policy.Policy

	admin *admin // remote administration: jobs and caches (admin.go)
	repos reposState

	mu        sync.Mutex
	runs      map[string]*run
	wg        sync.WaitGroup // running runs
	online    bool
	updating  bool
	startedAt time.Time

	// connected is signalled when a session is established (tests use it).
	connected chan struct{}

	tr    Transport  // how the brain is reached (transport.go)
	trust *nodeTrust // the zero-trust side: tunnel, guard, audit (trustnode.go)

	// OnUpdated is called after a new version replaced the executable; it
	// should restart the daemon (see cmd/sous). Nil: just log it.
	OnUpdated func(version string)
	// Exe is the executable an update replaces (default: os.Executable).
	Exe string
	// UpdateAPI overrides the GitHub API base URL (tests).
	UpdateAPI string
}

// NewDaemon builds a daemon. Agent overrides from the config replace the
// built-in launch commands.
func NewDaemon(root string, cfg Config, specs []agents.Spec) *Daemon {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	ob, err := openOutbox(filepath.Join(root, "outbox.jsonl"))
	if err != nil {
		slog.Error("daemon: cannot keep the outbox on disk; frames are lost if the daemon restarts", "err", err)
		ob = newOutbox()
	}
	d := &Daemon{root: root, base: specs, ob: ob, sem: make(chan struct{}, cfg.Concurrency),
		runs: map[string]*run{}, connected: make(chan struct{}, 1), startedAt: time.Now()}
	d.repos.kick = make(chan struct{}, 1)
	d.apply(cfg)
	d.admin = newAdmin(d)
	return d
}

// apply takes a config: the agents' launch overrides and environment, and
// the floor rules. Runs already started keep what they started with; the
// concurrency only changes with a restart.
func (d *Daemon) apply(cfg Config) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	pol, err := policy.New(cfg.Policy)
	if err != nil {
		slog.Error("daemon: invalid floor rule in node.json; using the default rules", "err", err)
		pol, _ = policy.New(policy.Config{})
	}
	specs := EffectiveSpecs(d.base, cfg)
	d.cfgMu.Lock()
	d.cfg, d.specs, d.policy = cfg, specs, pol
	d.cfgMu.Unlock()
}

// EffectiveSpecs applies node.json to the built-in agents: launch
// overrides replace the command, agent_env is added to the environment.
func EffectiveSpecs(base []agents.Spec, cfg Config) []agents.Spec {
	specs := make([]agents.Spec, len(base))
	copy(specs, base)
	for _, c := range cfg.CustomAgents {
		if err := validCustom(c, specs); err != nil {
			slog.Warn("daemon: skipping a custom agent in node.json", "name", c.Name, "err", err)
			continue
		}
		specs = append(specs, agents.Spec{Name: c.Name, Title: firstNonEmpty(c.Title, c.Name), Binary: c.Command[0],
			Launch: append([]string(nil), c.Command...), Custom: true})
	}
	for i, s := range specs {
		if cmd, ok := cfg.Agents[s.Name]; ok && len(cmd) > 0 {
			specs[i].Launch = cmd
		}
		if env := cfg.AgentEnv[s.Name]; len(env) > 0 {
			e := append([]string(nil), s.Env...)
			for _, k := range sortedKeys(env) {
				e = append(e, k+"="+env[k])
			}
			specs[i].Env = e
		}
	}
	return specs
}

var customName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// validCustom checks a custom agent from node.json.
func validCustom(c CustomAgent, have []agents.Spec) error {
	if !customName.MatchString(c.Name) {
		return fmt.Errorf("name must be lowercase letters, digits and -")
	}
	if _, dup := agents.Find(have, c.Name); dup {
		return fmt.Errorf("an agent named %q already exists", c.Name)
	}
	if len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
		return fmt.Errorf("command is empty")
	}
	return nil
}

func (d *Daemon) conf() Config {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.cfg
}

func (d *Daemon) agentSpecs() []agents.Spec {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.specs
}

func (d *Daemon) findSpec(name string) (agents.Spec, bool) { return agents.Find(d.agentSpecs(), name) }

func (d *Daemon) pol() *policy.Policy {
	d.cfgMu.RLock()
	defer d.cfgMu.RUnlock()
	return d.policy
}

// Run keeps a connection to the Hub until ctx ends, reconnecting with backoff.
func (d *Daemon) Run(ctx context.Context) error {
	defer func() {
		d.mu.Lock()
		nt := d.trust
		d.mu.Unlock()
		if nt != nil && nt.cancel != nil {
			nt.cancel() // leave the relay too
		}
	}()
	backoff := time.Second
	for {
		start := time.Now()
		err := d.session(ctx)
		if ctx.Err() != nil {
			d.stopAll(true)
			d.ob.close()
			return nil
		}
		if errors.Is(err, ErrRevoked) {
			d.stopAll(false)
			d.ob.close()
			return err
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		slog.Warn("daemon: disconnected from the brain", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			d.stopAll(true)
			d.ob.close()
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (d *Daemon) session(ctx context.Context) error {
	cfg := d.conf()
	t, err := d.transport()
	if err != nil {
		return err
	}
	ws, err := t.Dial(ctx)
	if err != nil {
		return err
	}
	defer ws.Close()

	host, _ := os.Hostname()
	if err := ws.Write(ctx, proto.New(proto.THello, proto.Hello{
		Version: version.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host,
		Caps: []string{proto.CapAdmin}, Admin: cfg.AdminEnabled(), Addrs: ownAddrs(),
	})); err != nil {
		return err
	}
	wctx, done := context.WithTimeout(ctx, welcomeTimeout)
	f, err := ws.Read(wctx)
	done()
	if err != nil {
		return fmt.Errorf("waiting for welcome: %w", err)
	}
	var welcome proto.Welcome
	if f.T != proto.TWelcome || f.Decode(&welcome) != nil {
		return fmt.Errorf("expected welcome, got %q", f.T)
	}

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	epoch := d.ob.connect()
	defer d.ob.disconnect()
	d.setOnline(true)
	defer d.setOnline(false)
	go d.writer(sctx, ws, epoch, stop)
	d.reconcile(welcome.ResumeRuns)
	go d.inventoryLoop(sctx)
	go d.reposLoop(sctx)
	go d.fetchLoop(sctx)
	slog.Info("daemon: connected to the brain", "via", t.Name(), "node", welcome.NodeID, "pending", d.ob.pendingCount())
	select {
	case d.connected <- struct{}{}:
	default:
	}

	for {
		f, err := ws.Read(sctx)
		if err != nil {
			return err
		}
		if !d.allow(sctx, f) {
			continue
		}
		d.handle(sctx, f)
	}
}

const maxFrameBytes = 4 << 20

func (d *Daemon) writer(ctx context.Context, ws FrameConn, epoch int, fail func()) {
	for {
		f, ok := d.ob.next(ctx, epoch)
		if !ok {
			return
		}
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := ws.Write(wctx, f)
		cancel()
		if err != nil {
			fail()
			return
		}
	}
}

func (d *Daemon) inventoryLoop(ctx context.Context) {
	t := time.NewTicker(inventoryEvery)
	defer t.Stop()
	for {
		d.sendInventory(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.admin.redetect:
		}
	}
}

// sendInventory detects the agents and reports them to the Hub.
func (d *Daemon) sendInventory(ctx context.Context) []proto.AgentInfo {
	agents.AugmentPath()
	infos := d.admin.describe(agents.Detect(ctx, d.agentSpecs()))
	if ctx.Err() != nil {
		return infos
	}
	cfg := d.conf()
	d.ob.reliable(proto.New(proto.TInventory, proto.Inventory{Agents: infos, Concurrency: cfg.Concurrency,
		Role: cfg.RoleName(), Workspaces: cfg.WorkspaceDirs(), Wok: wokVersion(ctx)}))
	return infos
}

func (d *Daemon) handle(ctx context.Context, f proto.Frame) {
	switch f.T {
	case proto.TAck:
		var a proto.Ack
		if f.Decode(&a) == nil {
			d.ob.ack(a.Seq)
		}
	case proto.TRunStart:
		var s proto.RunStart
		if f.Decode(&s) == nil {
			d.start(s)
		}
	case proto.TRunCancel:
		var c proto.RunCancel
		if f.Decode(&c) == nil {
			if r := d.run(c.RunID); r != nil {
				r.stop("用户取消")
			}
		}
	case proto.TRunMessage:
		var m proto.RunMessage
		if f.Decode(&m) == nil {
			if r := d.run(m.RunID); r != nil {
				r.followUp(m.Text, m.MessageID)
			}
		}
	case proto.TRunPause:
		var p proto.RunPause
		if f.Decode(&p) == nil {
			if r := d.run(p.RunID); r != nil {
				r.pause(p.Reason)
			}
		}
	case proto.TRunResume:
		var p proto.RunResume
		if f.Decode(&p) == nil {
			if r := d.run(p.RunID); r != nil {
				r.resume()
			}
		}
	case proto.TNodeRequest:
		var req proto.NodeRequest
		if f.Decode(&req) == nil {
			go d.admin.serve(ctx, f.ID, req)
		}
	case proto.TNodeUpdate:
		var u proto.NodeUpdate
		if f.Decode(&u) == nil {
			go d.selfUpdate(u)
		}
	case proto.TWorkspaceRemove:
		var w proto.WorkspaceRemove
		if f.Decode(&w) == nil {
			go d.removeWorkspace(w)
		}
	case proto.TApprovalDecide:
		var a proto.ApprovalDecide
		if f.Decode(&a) == nil {
			if r := d.run(a.RunID); r != nil {
				r.decide(a.ApprovalID, a.OptionID, a.Note)
			}
		}
	}
}

// removeWorkspace deletes a closed task's worktree, unless a run of the
// task is still here.
func (d *Daemon) removeWorkspace(w proto.WorkspaceRemove) {
	d.mu.Lock()
	for _, r := range d.runs {
		if r.spec.TaskID == w.TaskID {
			d.mu.Unlock()
			return
		}
	}
	d.mu.Unlock()
	if w.RepoPath == "" {
		return
	}
	if err := workspace.RemoveTask(context.Background(), d.root, w.RepoPath, w.TaskID); err != nil {
		slog.Warn("daemon: remove worktree", "task", w.TaskID, "err", err)
		return
	}
	slog.Debug("daemon: removed the worktree of a closed task (if it was still there)", "task", w.TaskID)
}

func (d *Daemon) run(id string) *run {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runs[id]
}

// reconcile compares the Hub's view with the runs on this machine. A run the
// Hub thinks is running but this daemon does not have was lost in a
// restart: it is resumed when its agent can load the session again, and
// reported interrupted otherwise. A run this daemon has that the Hub no
// longer lists was cancelled while the machine was away, unless the Hub
// simply has not stored its start yet.
func (d *Daemon) reconcile(resume []string) {
	want := map[string]bool{}
	saved := d.savedRuns()
	for _, id := range resume {
		want[id] = true
		if d.run(id) != nil {
			continue
		}
		if sv, ok := saved[id]; ok && sv.Spec.Purpose == proto.PurposeConsult {
			// A consultant is not resumed in the middle: it ends as idle
			// and the next question loads its session.
			d.forget(id)
			e := proto.RunEnded{RunID: id, Status: "succeeded", StopReason: "idle", Summary: "这台机器上的 sous 重启过，顾问已经停下；下次提问会接上会话"}
			if sv.CanLoad {
				e.SessionID = sv.SessionID
			}
			d.ob.reliable(proto.New(proto.TRunEnded, e))
			continue
		}
		if sv, ok := saved[id]; ok && sv.Spec.Purpose == proto.PurposeWork && (sv.Result != nil || sv.Completed) {
			// A persisted report survives a daemon restart too; do not send
			// another prompt after the agent already handed in its result.
			summary := ""
			if sv.Result != nil {
				summary = mask(resultSection(sv.Result))
			}
			end := workOutcome("interrupted", "", summary, "", errors.New("这台机器上的 sous 重启过，执行被中断了"), 0, false, sv.Result, sv.Completed)
			end.RunID, end.SessionID = id, sv.SessionID
			end.Error = mask(end.Error)
			d.ob.reliable(proto.New(proto.TRunEnded, end))
			d.forget(id)
			continue
		}
		if sv, ok := saved[id]; ok && sv.CanLoad && sv.SessionID != "" && sv.Spec.Purpose != proto.PurposeDeliver && sv.Spec.Purpose != proto.PurposeGit {
			slog.Info("daemon: resuming a run from before the restart", "run", id)
			d.restore(sv)
			continue
		}
		d.forget(id)
		d.ob.reliable(proto.New(proto.TRunEnded, proto.RunEnded{
			RunID: id, Status: "interrupted", Error: "这台机器上的 sous 重启过，执行被中断了",
		}))
	}
	for id := range saved {
		if !want[id] && d.run(id) == nil {
			d.forget(id) // the Hub ended it meanwhile
		}
	}
	d.mu.Lock()
	var stale []*run
	for id, r := range d.runs {
		if !want[id] && !d.ob.pendingFor(proto.TRunStarted, id) && r.started() {
			stale = append(stale, r)
		}
	}
	d.mu.Unlock()
	for _, r := range stale {
		r.stop("Hub 上已经取消")
	}
}

func (d *Daemon) start(s proto.RunStart) {
	d.mu.Lock()
	if _, exists := d.runs[s.RunID]; exists {
		d.mu.Unlock()
		return // the Hub sent it again after a reconnect
	}
	if m := policy.EffectiveMode(d.role(), s.PermissionMode); m != s.PermissionMode {
		slog.Info("daemon: this machine is a server; the open mode becomes default", "run", s.RunID)
		s.PermissionMode = m
	}
	// A consultant only reads and a reviewer only reads and runs safe
	// commands, whatever mode the Hub sent (decision 0022).
	switch s.Purpose {
	case proto.PurposeConsult:
		s.PermissionMode = proto.ModeReadOnly
	case proto.PurposeReview:
		s.PermissionMode = proto.ModeVerify
	}
	r := newRun(d, s)
	if s.Persona != "" && s.Purpose != proto.PurposeDeliver && s.Purpose != proto.PurposeGit {
		r.status("岗位：" + s.Persona)
	}
	d.launch(r)
}

// restore starts a saved run again; it loads the agent's session.
func (d *Daemon) restore(sv savedRun) {
	d.mu.Lock()
	if _, exists := d.runs[sv.Spec.RunID]; exists {
		d.mu.Unlock()
		return
	}
	r := newRun(d, sv.Spec)
	r.resumed = &sv
	r.sessionID, r.canLoad = sv.SessionID, sv.CanLoad
	r.paused, r.pauseReason = sv.Paused, sv.PauseReason
	r.delivered = append([]string(nil), sv.Delivered...)
	r.workBaseline, r.sous.result, r.sous.completed = sv.WorkBaseline, sv.Result, sv.Completed
	for _, id := range sv.Delivered {
		r.seen[id] = true
	}
	d.launch(r)
}

// launch registers and runs r; d.mu is held and released here.
func (d *Daemon) launch(r *run) {
	d.runs[r.spec.RunID] = r
	d.wg.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.wg.Done()
		r.execute()
		d.mu.Lock()
		delete(d.runs, r.spec.RunID)
		d.mu.Unlock()
	}()
}

// stopAllWait bounds how long shutdown waits for agents to exit.
const stopAllWait = 15 * time.Second

// stopAll ends every run at once, killing its agent, and waits until the
// agents have exited, so no agent process outlives the daemon. With keep
// the runs stay saved, to be resumed when the daemon starts again.
func (d *Daemon) stopAll(keep bool) {
	d.admin.stopJobs()
	d.mu.Lock()
	runs := make([]*run, 0, len(d.runs))
	for _, r := range d.runs {
		runs = append(runs, r)
	}
	d.mu.Unlock()
	for _, r := range runs {
		r.kill("sous daemon 已停止", keep)
	}
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopAllWait):
		slog.Warn("daemon: agents did not exit in time")
	}
}

// ownAddrs lists the machine's IP addresses, for the Hub to recognize it.
func ownAddrs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsMulticast() {
			continue
		}
		out = append(out, ipn.IP.String())
		if len(out) == 16 {
			break
		}
	}
	return out
}

// Package hub wires the Hub's parts together. It holds no domain rules.
package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/effects"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/externalmcp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/inspector"
	"github.com/XiaoXianThis/Sous/apps/core/internal/localdispatch"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/notify"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trigger"
	"github.com/XiaoXianThis/Sous/apps/core/internal/update"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
	"github.com/XiaoXianThis/Sous/apps/core/internal/watchdog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/web"
)

type Hub struct {
	Cfg             *config.Hub
	Store           *store.Store
	Log             *eventlog.Log
	Auth            *auth.Auth
	module          AgentModule
	bindings        *agentBindings
	lock            *os.File
	Effects         *effects.Runner
	Nodes           *nodehub.Hub
	Tasks           *task.Engine
	Notifier        *notify.Notifier
	Triggers        *trigger.Scheduler
	Inspector       *inspector.Inspector
	Updates         *update.Checker
	Watchdog        *watchdog.Watchdog
	HTTP            *httpapi.Server
	SandboxDispatch *localdispatch.Queue
	CloudWork       *cloudwork.Queue
	// WatchdogOn makes a stuck process exit so its service manager restarts
	// it. The sous-hub command turns it on; tests leave it off.
	WatchdogOn bool

	// Brain is set when the Hub runs inside a brain machine's daemon (0.3):
	// no setup link, no page, and ConnContext tags requests that came
	// through the relay. Started is closed once it serves.
	Brain       bool
	ConnContext func(ctx context.Context, c net.Conn) context.Context
	Started     chan struct{}
	LocalReady  func(origin, setupURL string) error
}

// DBPath is where the Hub keeps its database inside the data directory.
func DBPath(cfg *config.Hub) string { return filepath.Join(cfg.DataDir, "hub.db") }

// Open builds a workspace. A nil factory leaves it fully usable without AI.
// The caller chooses the optional Agent module; Hub never creates one itself.
func Open(cfg *config.Hub, factory AgentFactory) (*Hub, error) { return open(cfg, factory, nil) }

// OpenLocalWithAgents enables live binding selection from a fixed local catalog.
func OpenLocalWithAgents(cfg *config.Hub, catalog LocalAgents) (*Hub, error) {
	return open(cfg, nil, &catalog)
}

func open(cfg *config.Hub, factory AgentFactory, catalog *LocalAgents) (_ *Hub, err error) {
	if cfg.HostedWorkspace && runtime.GOOS == "windows" {
		return nil, errHostedOnWindows
	}
	var lock *os.File
	if cfg.DirectWorkspace() {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		lock, err = lockWorkspace(cfg.DataDir)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				lock.Close()
			}
		}()
	}
	if cfg.HostedWorkspace {
		if _, err := httpapi.HostedShell(cfg.WebDir); err != nil {
			return nil, err
		}
		if err := verifyHostedDirectory(cfg); err != nil {
			return nil, err
		}
	}
	identity, err := preflightWorkspace(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	sysmap.Reset()
	notify.RegisterSystemProbes()
	sysmap.RegisterProbe("web", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		if cfg.Web.Search.Provider != "" {
			return sysmap.Status{State: sysmap.OK, Summary: "搜索：" + cfg.Web.Search.Provider}
		}
		return sysmap.Status{State: sysmap.Idle, Summary: "没有配置搜索引擎，只能抓取网页"}
	}))
	sysmap.RegisterProbe("search", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		state := sysmap.Idle
		if sysmap.Sum("web>search", 60) > 0 {
			state = sysmap.Busy
		}
		return sysmap.Status{State: state, Summary: fmt.Sprintf("最近 15 分钟 %d 次", sysmap.Sum("web>search", 900))}
	}))
	var st *store.Store
	switch {
	case identity.initialize:
		st, err = store.OpenInitialized(DBPath(cfg), identity.key, identity.value, cfg.HostedWorkspace)
	case cfg.HostedWorkspace:
		st, err = store.OpenDurable(DBPath(cfg))
	default:
		st, err = store.Open(DBPath(cfg))
	}
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if identity.adopt { // positive marker from an earlier Desktop preview
		if err := st.SetSetting(context.Background(), identity.key, identity.value); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	h := &Hub{Cfg: cfg, Store: st, lock: lock}
	h.Log = eventlog.New(st, event.NewBus())
	if h.Auth, err = auth.New(st, cfg); err != nil {
		st.Close()
		return nil, err
	}
	if h.Nodes, err = nodehub.New(h.Log, cfg.DataDir); err != nil {
		st.Close()
		return nil, err
	}
	h.Nodes.Owner, h.Nodes.Fresh = h.Auth.Owner, h.Auth.RequireFresh
	h.Watchdog = watchdog.New()
	h.Tasks = task.New(h.Log, h.Nodes)
	h.Tasks.PlanningOnly = cfg.HostedWorkspace
	h.Tasks.ApprovalPolicy = task.ApprovalPolicy{AllowMedium: cfg.ApprovalPolicy.AllowMedium, MaxAIAllowsPerRun: cfg.ApprovalPolicy.MaxAIAllowsPerRun}
	h.Nodes.MaxAIAllowsPerRun = cfg.ApprovalPolicy.MaxAIAllowsPerRun
	h.Tasks.Beat = h.Watchdog.Loop("task engine", 5*time.Minute)

	tc := cfg.Triggers
	h.Triggers = trigger.New(h.Log, trigger.Options{
		FollowupEvery: tc.FollowupEvery, BlockedAfter: tc.BlockedAfter, RunTimeout: tc.RunTimeout,
		ApprovalAfter: tc.ApprovalAfter, QuestionAfter: tc.QuestionAfter,
		RetentionDays: cfg.RetentionDays, Archive: h.Nodes.Archive,
		Beat: h.Watchdog.Loop("triggers", 5*time.Minute),
	})
	h.Updates = &update.Checker{Client: update.Client{Repo: cfg.UpdateRepo}}

	tools := tool.NewRegistry(
		tool.CurrentTime{},
		tool.MemorySave{Log: h.Log}, tool.MemorySearch{Log: h.Log}, tool.MemoryForget{Log: h.Log},
		tool.Notify{Log: h.Log},
		tool.ThreadSearch{Log: h.Log}, tool.ThreadRead{Log: h.Log},
	)
	for _, t := range append(h.Tasks.Tools(), h.Triggers.Tools()...) {
		tools.Register(t)
	}
	for _, t := range webTools(cfg) {
		tools.Register(t)
	}

	vapid, err := notify.LoadVAPID(cfg.DataDir)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("web push keys: %w", err)
	}
	h.Notifier = &notify.Notifier{Log: h.Log, Sender: &notify.Sender{VAPID: vapid, Subject: pushSubject(cfg)}}

	deps := AgentDeps{Config: cfg, Log: h.Log, Tools: tools, Tasks: h.Tasks, Nodes: h.Nodes, Auth: h.Auth}
	if cfg.HostedWorkspace && catalog != nil {
		_ = st.Close()
		return nil, errors.New("hosted supervisor is operator-configured")
	}
	if catalog != nil {
		h.bindings, err = newBindings(context.Background(), deps, *catalog)
		if err != nil {
			st.Close()
			return nil, err
		}
		h.module = h.bindings.module()
	} else if factory != nil {
		module, err := factory(deps)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("attach agent module: %w", err)
		}
		if cfg.HostedWorkspace && module.Name != "acp" {
			_ = st.Close()
			return nil, errors.New("hosted workspace only supports the bundled ACP supervisor")
		}
		h.module = module
	}
	h.Tasks.Reviewer, h.Tasks.Approver = h.module.Reviewer, h.module.Approver
	ic := cfg.Inspector
	every := ic.Every
	if every <= 0 {
		every = 10 * time.Minute
	}
	h.Inspector = inspector.New(h.Log, h.Nodes, h.module.Inspector.Provider, h.module.Inspector.Name, inspector.Options{
		Every: every, Repeat: ic.Repeat, StalledAfter: ic.StalledAfter, LongAfter: ic.LongAfter, TokenAlarm: ic.TokenAlarm,
		Beat: h.Watchdog.Loop("inspector", every+5*time.Minute),
	})

	h.Effects = effects.NewRunner(st, h.Log.EffectsEnqueued(), 2*time.Second)
	h.Effects.Beat = h.Watchdog.Loop("effects", 2*time.Minute)
	h.Effects.Handle(store.EffectTaskAdvance, h.Tasks.Advance)
	h.Effects.Handle(store.EffectApprove, h.Tasks.Approve)
	h.Effects.Handle(store.EffectSetup, h.Tasks.Setup)
	if cfg.DirectWorkspace() {
		// Old queued notifications must not leave an offline workspace.
		h.Effects.Handle(store.EffectPush, func(context.Context, store.Effect) error { return nil })
	} else {
		h.Effects.Handle(store.EffectPush, h.Notifier.Handle)
	}
	origins := []string{}
	if u, err := url.Parse(cfg.PublicURL); err == nil {
		origins = append(origins, u.Host)
	}
	if cfg.Dev {
		origins = append(origins, "localhost:*", "127.0.0.1:*")
	}
	sysroutes := func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		sysmap.Routes(mux, protect, origins, sysmap.TraceDeps{RunsOf: func(ctx context.Context, id string) []string {
			runs, _ := st.Runs(ctx, store.RunFilter{TaskID: id, Limit: 2147483647})
			out := make([]string, 0, len(runs))
			for _, run := range runs {
				out = append(out, run.ID)
			}
			return out
		}, PendingOf: func(ctx context.Context, kind, id string) bool {
			switch kind {
			case "task":
				t, err := st.Task(ctx, id)
				return err == nil && (t.Status == "running" || t.Status == "queued" || t.Status == "waiting_approval")
			case "run":
				r, err := st.Run(ctx, id)
				return err == nil && r.Status == "running"
			case "thread":
				return h.module.Active != nil && h.module.Active(id)
			}
			return false
		}})
	}
	nodeRoutes := h.Nodes.Routes
	if cfg.HostedWorkspace {
		nodeRoutes = h.Nodes.HostedPlanningRoutes
	} else if cfg.LocalWorkspace {
		nodeRoutes = h.Nodes.WorkspaceRoutes
	}
	routes := []httpapi.Routes{sysroutes, nodeRoutes, h.Tasks.Routes, h.Triggers.Routes, h.Inspector.Routes, h.capabilityRoutes}
	if cfg.HostedWorkspace && len(cfg.ExternalMCPClients) > 0 {
		inbound := externalmcp.New(st, h.Tasks, cfg.PublicURL, cfg.ExternalMCPClients)
		routes = append(routes, inbound.Routes(h.Auth.RequireFresh))
	}
	if cfg.HostedWorkspace && (cfg.CloudWorkerToken != "" || cfg.CloudControllerToken != "") {
		if cfg.CloudControllerToken != "" {
			h.CloudWork, err = cloudwork.NewManaged(context.Background(), st, cfg.CloudControllerToken, cfg.CloudIdleTimeout)
		} else {
			h.CloudWork, err = cloudwork.New(context.Background(), st, cfg.CloudWorkerToken)
		}
		if err != nil {
			_ = st.Close()
			return nil, err
		}
		routes = append(routes, h.CloudWork.Routes(h.Auth.RequireFresh))
	}
	if cfg.DirectWorkspace() {
		if cfg.LocalWorkspace && cfg.LocalSandboxDispatch {
			h.SandboxDispatch = localdispatch.New(st)
			if err := h.SandboxDispatch.Recover(context.Background()); err != nil {
				_ = st.Close()
				return nil, fmt.Errorf("recover sandbox dispatch: %w", err)
			}
			routes = append(routes, h.SandboxDispatch.Routes)
		}
		routes = append(routes, h.Notifier.SettingsRoutes(h.module.roleRefs))
	} else {
		routes = append(routes, h.Notifier.Routes(h.module.roleRefs), h.Updates.Routes(st, h.Nodes))
	}
	routes = append(routes, h.module.Routes...)
	if h.bindings != nil {
		routes = append(routes, h.bindings.Routes)
	}
	h.HTTP = httpapi.New(cfg, h.Log, h.Auth, routes...)
	if h.bindings != nil {
		h.HTTP.SetAPIFallback(h.bindings)
	}
	if !cfg.DirectWorkspace() {
		h.HTTP.DeviceRoutes(h.Nodes.DeviceAuth)
		h.Nodes.DeviceRoutes(h.HTTP.Mux())
	}
	// The single writer's lock must never hang: every write goes through it.
	h.Watchdog.Probe("store", 2*time.Minute, func(ctx context.Context) error {
		_, err := h.Log.Write(ctx, func(*store.Writer) error { return nil })
		return err
	})
	return h, nil
}

// pushSubject is the contact push services see in the VAPID token.
func pushSubject(cfg *config.Hub) string {
	if strings.HasPrefix(cfg.PublicURL, "https://") {
		return cfg.PublicURL
	}
	return "mailto:sous@localhost"
}

// Close releases resources on a Hub that has stopped or was never started.
func (h *Hub) Close() error {
	err := h.Store.Close()
	if h.lock != nil {
		err = errors.Join(err, h.lock.Close())
	}
	return err
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (h *Hub) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", h.Cfg.Listen)
	if err != nil {
		h.Close()
		return err
	}
	return h.RunOn(ctx, ln)
}

// RunOn runs the Hub serving its HTTP API on ln. The brain (internal/brain)
// passes an in-memory listener: in 0.3 nothing reaches the API except
// through the relay.
func (h *Hub) RunOn(ctx context.Context, ln net.Listener) error {
	defer h.Close()
	defer ln.Close()
	if _, err := h.Log.EnsureMainThread(ctx); err != nil {
		return err
	}
	if !h.Brain && !h.Cfg.HostedWorkspace {
		var setup string
		if !h.Cfg.LocalDesktopAuth {
			var err error
			setup, err = h.setupURL(ctx)
			if err != nil {
				return err
			}
		}
		if h.LocalReady != nil {
			if err := h.LocalReady(h.Cfg.PublicURL, setup); err != nil {
				return err
			}
		} else if setup != "" {
			slog.Info("no passkey registered yet; open this link within 24 hours to register one", "url", setup)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Seed Hub domain data before an attached module resumes saved turns.
	h.Tasks.Start(runCtx)
	if h.module.Start != nil {
		h.module.Start(runCtx)
	}
	if h.module.Wait != nil {
		defer func() {
			cancel()
			h.module.Wait()
		}()
	}
	h.Triggers.Start(runCtx)
	h.Inspector.Start(runCtx)
	effectsDone := make(chan struct{})
	go func() {
		defer close(effectsDone)
		h.Effects.Run(runCtx)
	}()
	defer func() {
		cancel()
		<-effectsDone
	}()
	if h.WatchdogOn {
		go h.Watchdog.Run(runCtx)
	}

	srv := &http.Server{Handler: h.HTTP, ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ConnContext: h.ConnContext,
		BaseContext: func(net.Listener) context.Context { return runCtx }}
	errCh := make(chan error, 1)
	go func() {
		if h.Cfg.TLS() && !h.Brain {
			errCh <- srv.ServeTLS(ln, h.Cfg.TLSCert, h.Cfg.TLSKey)
			return
		}
		errCh <- srv.Serve(ln)
	}()
	if h.Brain {
		slog.Info("brain: thinking", "commit", version.BuildCommit())
	} else {
		slog.Info("sous-hub listening", "addr", h.Cfg.Listen, "public_url", h.Cfg.PublicURL, "dev", h.Cfg.Dev, "commit", version.BuildCommit())
		logWebBuild(h.Cfg.WebDir)
	}
	if h.Started != nil {
		close(h.Started)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	return srv.Shutdown(shutdownCtx)
}

func (h *Hub) setupURL(ctx context.Context) (string, error) {
	has, err := h.Auth.HasPasskey(ctx)
	if err != nil || has {
		return "", err
	}
	tok, err := h.Auth.NewSetupToken(ctx)
	if err != nil {
		return "", err
	}
	return h.Auth.SetupURL(tok), nil
}

// webTools returns web_search and web_fetch unless the config turns them off.
// A search engine that cannot start (no key) is left out with a warning, so
// the model is never offered a tool that always fails.
func webTools(cfg *config.Hub) []tool.Tool {
	if cfg.Web.Disabled {
		return nil
	}
	tools := []tool.Tool{tool.WebFetch{Fetcher: &web.Fetcher{}}}
	s, err := web.NewSearcher(web.SearchConfig{
		Provider: cfg.Web.Search.Provider, APIKey: cfg.Web.Search.APIKey(), BaseURL: cfg.Web.Search.BaseURL,
	})
	if err != nil {
		slog.Warn("web_search is off", "err", err, "hint", "set the key in the environment variable named by web.search.api_key_env")
		return tools
	}
	slog.Info("web_search ready", "engine", s.Name())
	return append(tools, tool.WebSearch{Searcher: s})
}

// logWebBuild says which build of the page the Hub serves, and warns when it
// is not from the Hub's own commit: a stale build/web (review 4.23).
func logWebBuild(dir string) {
	if dir == "" {
		return
	}
	web, ok := version.ReadWeb(dir)
	if !ok {
		slog.Warn("sous-hub: the page has no build-info.json; it was built before build info existed, rebuild it with `wok build web`", "web_dir", dir)
		return
	}
	slog.Info("sous-hub: serving the page", "web_commit", web.Commit, "web_dirty", web.Dirty, "built_at", web.BuiltAt)
	hub := strings.TrimSuffix(version.BuildCommit(), "-dirty")
	if hub != "" && web.Commit != "" && !strings.HasPrefix(hub, web.Commit) && !strings.HasPrefix(web.Commit, hub) {
		slog.Warn("sous-hub: the page was built from another commit than the Hub; rebuild it with `wok build web`", "hub_commit", hub, "web_commit", web.Commit)
	}
}

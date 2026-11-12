// Package hub wires the Hub's parts together. It holds no domain rules.
package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/XiaoXianThis/Sous/apps/core/internal/mcp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/models"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/attach"
	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/effects"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/inspector"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/notify"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/supervisor"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trigger"
	"github.com/XiaoXianThis/Sous/apps/core/internal/update"
	"github.com/XiaoXianThis/Sous/apps/core/internal/watchdog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/web"
)

type Hub struct {
	Cfg        *config.Hub
	Store      *store.Store
	Log        *eventlog.Log
	Auth       *auth.Auth
	Models     *models.Registry
	MCP        *mcp.Manager
	Supervisor *supervisor.Supervisor
	Effects    *effects.Runner
	Nodes      *nodehub.Hub
	Tasks      *task.Engine
	Notifier   *notify.Notifier
	Triggers   *trigger.Scheduler
	Inspector  *inspector.Inspector
	Updates    *update.Checker
	Watchdog   *watchdog.Watchdog
	HTTP       *httpapi.Server
	// WatchdogOn makes a stuck process exit so its service manager restarts
	// it. The sous-hub command turns it on; tests leave it off.
	WatchdogOn bool

	// Brain is set when the Hub runs inside a brain machine's daemon (0.3):
	// no setup link, no page, and ConnContext tags requests that came
	// through the relay. Started is closed once it serves.
	Brain       bool
	ConnContext func(ctx context.Context, c net.Conn) context.Context
	Started     chan struct{}
}

// DBPath is where the Hub keeps its database inside the data directory.
func DBPath(cfg *config.Hub) string { return filepath.Join(cfg.DataDir, "hub.db") }

// Open builds every part from the config. Call Run to start serving.
func Open(cfg *config.Hub) (*Hub, error) {
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
	st, err := store.Open(DBPath(cfg))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	h := &Hub{Cfg: cfg, Store: st}
	h.Log = eventlog.New(st, event.NewBus())
	if h.Auth, err = auth.New(st, cfg); err != nil {
		st.Close()
		return nil, err
	}
	// Models are configured on the page and kept in the database; hub.yaml's
	// providers and roles are imported on the first start.
	h.Models = models.New(h.Log)
	h.Models.Wrap = func(p provider.Provider) provider.Provider { return provider.WithRetry(p, 3, 2*time.Second) }
	if err := h.Models.Seed(context.Background(), cfg); err != nil {
		st.Close()
		return nil, fmt.Errorf("import models from hub.yaml: %w", err)
	}
	if !h.Models.Configured(context.Background(), config.RoleSupervisor) {
		slog.Warn("no model is configured for the supervisor yet: add a provider in Settings → Models")
	}
	if h.Nodes, err = nodehub.New(h.Log, cfg.DataDir); err != nil {
		st.Close()
		return nil, err
	}
	h.Nodes.Owner, h.Nodes.Fresh = h.Auth.Owner, h.Auth.RequireFresh
	// Each role's provider looks up its current model on every call.
	role := func(r string) task.Model { return task.Model{Provider: h.Models.For(r), Name: r} }
	h.Watchdog = watchdog.New()
	h.Tasks = task.New(h.Log, h.Nodes)
	h.Tasks.ApprovalPolicy = task.ApprovalPolicy{AllowMedium: cfg.ApprovalPolicy.AllowMedium, MaxAIAllowsPerRun: cfg.ApprovalPolicy.MaxAIAllowsPerRun}
	h.Nodes.MaxAIAllowsPerRun = cfg.ApprovalPolicy.MaxAIAllowsPerRun
	h.Tasks.Reviewer, h.Tasks.Approver = role(config.RoleReviewer), role(config.RoleApprover)
	h.Tasks.Beat = h.Watchdog.Loop("task engine", 5*time.Minute)

	tc := cfg.Triggers
	h.Triggers = trigger.New(h.Log, trigger.Options{
		FollowupEvery: tc.FollowupEvery, BlockedAfter: tc.BlockedAfter, RunTimeout: tc.RunTimeout,
		ApprovalAfter: tc.ApprovalAfter, QuestionAfter: tc.QuestionAfter,
		RetentionDays: cfg.RetentionDays, Archive: h.Nodes.Archive,
		Beat: h.Watchdog.Loop("triggers", 5*time.Minute),
	})
	ic := cfg.Inspector
	insp := role(config.RoleInspector)
	every := ic.Every
	if every <= 0 {
		every = 10 * time.Minute
	}
	h.Inspector = inspector.New(h.Log, h.Nodes, insp.Provider, insp.Name, inspector.Options{
		Every: every, Repeat: ic.Repeat, StalledAfter: ic.StalledAfter, LongAfter: ic.LongAfter, TokenAlarm: ic.TokenAlarm,
		Beat: h.Watchdog.Loop("inspector", every+5*time.Minute),
	})
	h.Updates = &update.Checker{Client: update.Client{Repo: cfg.UpdateRepo}}

	sup := role(config.RoleSupervisor)
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
	if !cfg.MCP.Disabled {
		h.MCP = mcp.NewManager(h.Log, tools)
		h.MCP.StdioDisabled = cfg.MCP.StdioDisabled
		for _, t := range h.MCP.Tools() {
			tools.Register(t)
		}
	}
	h.Supervisor = supervisor.New(supervisor.Options{
		Log:      h.Log,
		Provider: sup.Provider,
		Fixed:    h.Models.Fixed,
		Model:    sup.Name,
		Tools:    tools,
		Environment: func(ctx context.Context, threadID string) []string {
			return append(h.Nodes.Environment(ctx), h.Tasks.Environment(ctx, threadID)...)
		},
		DescribeContext: h.Tasks.DescribeContext,
		Attachments:     attach.Open(filepath.Join(cfg.DataDir, "attachments")),
		Compactor:       h.Models.For(config.RoleCompactor),
		CompactorModel:  config.RoleCompactor,
		ContextWindow: func(ctx context.Context) int {
			return h.Models.ContextWindow(ctx, config.RoleSupervisor)
		},
		Gather: supervisor.DefaultGather,
	})

	vapid, err := notify.LoadVAPID(cfg.DataDir)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("web push keys: %w", err)
	}
	h.Notifier = &notify.Notifier{Log: h.Log, Sender: &notify.Sender{VAPID: vapid, Subject: pushSubject(cfg)}}

	h.Effects = effects.NewRunner(st, h.Log.EffectsEnqueued(), 2*time.Second)
	h.Effects.Beat = h.Watchdog.Loop("effects", 2*time.Minute)
	h.Effects.Handle(store.EffectTaskAdvance, h.Tasks.Advance)
	h.Effects.Handle(store.EffectApprove, h.Tasks.Approve)
	h.Effects.Handle(store.EffectSetup, h.Tasks.Setup)
	h.Effects.Handle(store.EffectPush, h.Notifier.Handle)
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
				return h.Supervisor.Active(id)
			}
			return false
		}})
	}
	h.HTTP = httpapi.New(cfg, h.Log, h.Auth, sysroutes, h.Nodes.Routes, h.Tasks.Routes, h.Notifier.Routes(h.Models.RoleRefs), h.Models.Routes(h.Auth.RequireFresh),
		h.mcpRoutes(), h.Triggers.Routes, h.Inspector.Routes, h.Updates.Routes(st, h.Nodes), h.Supervisor.Routes)
	h.HTTP.DeviceRoutes(h.Nodes.DeviceAuth)
	h.Nodes.DeviceRoutes(h.HTTP.Mux())
	// The single writer's lock must never hang: every write goes through it.
	h.Watchdog.Probe("store", 2*time.Minute, func(ctx context.Context) error {
		_, err := h.Log.Write(ctx, func(*store.Writer) error { return nil })
		return err
	})
	return h, nil
}

// mcpRoutes serves the MCP settings page's API unless MCP is switched off.
func (h *Hub) mcpRoutes() httpapi.Routes {
	if h.MCP == nil {
		return func(*http.ServeMux, func(string, http.HandlerFunc)) {}
	}
	return h.MCP.Routes(h.Auth.RequireFresh)
}

// pushSubject is the contact push services see in the VAPID token.
func pushSubject(cfg *config.Hub) string {
	if strings.HasPrefix(cfg.PublicURL, "https://") {
		return cfg.PublicURL
	}
	return "mailto:sous@localhost"
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (h *Hub) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", h.Cfg.Listen)
	if err != nil {
		return err
	}
	return h.RunOn(ctx, ln)
}

// RunOn runs the Hub serving its HTTP API on ln. The brain (internal/brain)
// passes an in-memory listener: in 0.3 nothing reaches the API except
// through the relay.
func (h *Hub) RunOn(ctx context.Context, ln net.Listener) error {
	defer h.Store.Close()
	if _, err := h.Log.EnsureMainThread(ctx); err != nil {
		return err
	}
	if !h.Brain {
		if err := h.announceSetup(ctx); err != nil {
			return err
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	h.Supervisor.Start(runCtx)
	if h.MCP != nil {
		if err := h.MCP.Start(runCtx); err != nil {
			slog.Warn("mcp: cannot start the servers", "err", err)
		}
	}
	h.Tasks.Start(runCtx)
	h.Triggers.Start(runCtx)
	h.Inspector.Start(runCtx)
	go h.Effects.Run(runCtx)
	if h.WatchdogOn {
		go h.Watchdog.Run(runCtx)
	}

	srv := &http.Server{Handler: h.HTTP, ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ConnContext: h.ConnContext}
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
			cancel()
			h.Supervisor.Wait()
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	err := srv.Shutdown(shutdownCtx)
	cancel()
	h.Supervisor.Wait()
	if h.MCP != nil {
		h.MCP.Stop()
	}
	return err
}

func (h *Hub) announceSetup(ctx context.Context) error {
	has, err := h.Auth.HasPasskey(ctx)
	if err != nil || has {
		return err
	}
	tok, err := h.Auth.NewSetupToken(ctx)
	if err != nil {
		return err
	}
	slog.Info("no passkey registered yet; open this link within 24 hours to register one", "url", h.Auth.SetupURL(tok))
	return nil
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

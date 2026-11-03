// Package hub wires the Hub's parts together. It holds no domain rules.
package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/effects"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/supervisor"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

type Hub struct {
	Cfg        *config.Hub
	Store      *store.Store
	Log        *eventlog.Log
	Auth       *auth.Auth
	Supervisor *supervisor.Supervisor
	Effects    *effects.Runner
	HTTP       *httpapi.Server
}

// DBPath is where the Hub keeps its database inside the data directory.
func DBPath(cfg *config.Hub) string { return filepath.Join(cfg.DataDir, "hub.db") }

// Open builds every part from the config. Call Run to start serving.
func Open(cfg *config.Hub) (*Hub, error) {
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
	providers := map[string]provider.Provider{}
	for _, pc := range cfg.Providers {
		p, err := provider.New(provider.Config{Name: pc.Name, Kind: pc.Kind, BaseURL: pc.BaseURL, APIKey: pc.APIKey()})
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("provider %s: %w", pc.Name, err)
		}
		if pc.Kind != "mock" && pc.APIKey() == "" {
			slog.Warn("provider has no API key", "provider", pc.Name, "env", pc.APIKeyEnv)
		}
		providers[pc.Name] = provider.WithRetry(p, 3, 2*time.Second)
	}
	provName, model := cfg.Model(config.RoleSupervisor)
	h.Supervisor = supervisor.New(supervisor.Options{
		Log:      h.Log,
		Provider: providers[provName],
		Model:    model,
		Tools:    tool.NewRegistry(tool.CurrentTime{}),
	})
	h.Effects = effects.NewRunner(st, h.Log.EffectsEnqueued(), 2*time.Second)
	h.HTTP = httpapi.New(cfg, h.Log, h.Auth)
	return h, nil
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (h *Hub) Run(ctx context.Context) error {
	defer h.Store.Close()
	if _, err := h.Log.EnsureMainThread(ctx); err != nil {
		return err
	}
	if err := h.announceSetup(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	h.Supervisor.Start(runCtx)
	go h.Effects.Run(runCtx)

	srv := &http.Server{Addr: h.Cfg.Listen, Handler: h.HTTP, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("sous-hub listening", "addr", h.Cfg.Listen, "public_url", h.Cfg.PublicURL, "dev", h.Cfg.Dev)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	err := srv.Shutdown(shutdownCtx)
	cancel()
	h.Supervisor.Wait()
	return err
}

// announceSetup prints a one-time registration link while no passkey exists.
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

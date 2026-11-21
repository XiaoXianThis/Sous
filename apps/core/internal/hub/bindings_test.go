package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestAgentBindingPreservesActiveUntilNewModulePassesProbeAndIdleBarrier(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var legacyStarts, legacyStops, acpStarts, acpStops atomic.Int32
	failProbe, busy := true, true
	var active context.Context
	catalog := LocalAgents{Default: "legacy", Choices: map[string]AgentFactory{
		"legacy": func(AgentDeps) (AgentModule, error) {
			return AgentModule{Name: "legacy", Start: func(c context.Context) { legacyStarts.Add(1); active = c }, Wait: func() {
				legacyStops.Add(1)
				if active.Err() == nil {
					t.Error("outgoing context not revoked")
				}
			}, PauseIfIdle: func() bool { return !busy }, Resume: func() {}}, nil
		},
		"acp": func(AgentDeps) (AgentModule, error) {
			return AgentModule{Name: "acp", Identity: "fixture-profile", Start: func(context.Context) {
				acpStarts.Add(1)
				if legacyStops.Load() != 1 {
					t.Error("new module started before old exited")
				}
			}, Wait: func() { acpStops.Add(1) }, PauseIfIdle: func() bool { return true }, Resume: func() {}, Probe: func(context.Context) error {
				if failProbe {
					return errors.New("fixture probe rejection")
				}
				return nil
			}}, nil
		},
	}}
	deps := AgentDeps{Config: config.ForLocalWorkspace(t.TempDir()), Log: eventlog.New(st, event.NewBus())}
	b, err := newBindings(ctx, deps, catalog)
	if err != nil {
		t.Fatal(err)
	}
	b.start(ctx)
	defer b.stop()
	next := bindingRequest{Selection: "acp", Generation: 1}
	if _, err := b.selectAgent(ctx, next); !errors.Is(err, errBindingUnavailable) {
		t.Fatal(err)
	}
	failProbe = false
	if _, err := b.selectAgent(ctx, next); !errors.Is(err, errBindingBusy) {
		t.Fatal(err)
	}
	if before := b.status(ctx); before.Module != "legacy" || before.Generation != 1 || legacyStops.Load() != 0 || acpStarts.Load() != 0 {
		t.Fatalf("changed after failure: %+v", before)
	}
	busy = false
	if got, err := b.selectAgent(ctx, next); err != nil || got.Module != "acp" || got.Generation != 2 || legacyStarts.Load() != 1 || acpStarts.Load() != 1 {
		t.Fatalf("cutover: %+v %v", got, err)
	}
	if _, err := b.selectAgent(ctx, next); !errors.Is(err, errBindingStale) {
		t.Fatalf("stale binding: %v", err)
	}
	reopened, err := newBindings(ctx, deps, catalog)
	if err != nil || reopened.status(ctx).Generation != 2 || reopened.status(ctx).Module != "acp" {
		t.Fatalf("restart did not load binding: %v", err)
	}
	catalog.Choices["acp"] = func(AgentDeps) (AgentModule, error) { return AgentModule{}, errors.New("model configuration removed") }
	reopened, err = newBindings(ctx, deps, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.status(ctx); got.Module != "" || got.Selection != "acp" || got.Notice == "" {
		t.Fatalf("not fail-closed: %+v", got)
	}
	if got, err := b.selectAgent(ctx, bindingRequest{Selection: "none", Generation: 2}); err != nil || got.State != "unbound" || got.Generation != 3 || acpStops.Load() != 1 {
		t.Fatalf("detach: %+v %v", got, err)
	}
}

func TestBindingRoutesDoNotOutliveSelectedModule(t *testing.T) {
	cfg := config.ForLocalWorkspace(t.TempDir())
	h, err := OpenLocalWithAgents(cfg, LocalAgents{Default: "legacy", Choices: map[string]AgentFactory{
		"legacy": func(AgentDeps) (AgentModule, error) {
			return AgentModule{Name: "legacy", Start: func(context.Context) {}, Wait: func() {}, PauseIfIdle: func() bool { return true }, Resume: func() {}, Routes: []httpapi.Routes{func(_ *http.ServeMux, protect func(string, http.HandlerFunc)) {
				protect("GET /api/v1/threads/{id}/binding-fixture", func(w http.ResponseWriter, r *http.Request) {
					if r.PathValue("id") != "hello" {
						t.Error("route parameters were lost")
					}
					w.WriteHeader(http.StatusNoContent)
				})
			}}}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.bindings.start(ctx)
	defer h.bindings.stop()
	call := func(method, path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.HTTP.ServeHTTP(w, httptest.NewRequest(method, cfg.PublicURL+path, nil))
		if w.Code != want {
			t.Fatalf("%s %s: %d (%s), want %d", method, path, w.Code, w.Body, want)
		}
	}
	call("PUT", "/api/v1/supervisor/binding", 401)
	call("GET", "/api/v1/threads/hello/binding-fixture", 401)
	if _, err := h.bindings.selectAgent(ctx, bindingRequest{Selection: "none", Generation: 1}); err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/v1/threads/hello/binding-fixture", 404)
}

package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

const bindingKey = "workspace.supervisor_binding"

var (
	errBindingBusy        = errors.New("主管仍有工作正在处理，请等待本轮完成后重试")
	errBindingStale       = errors.New("主管已在另一处切换，请刷新后重试")
	errBindingUnavailable = errors.New("所选主管不可用，现有绑定未改变")
	errBindingDisabled    = errors.New("当前没有配置这一模型能力")
)

// LocalAgents is trusted assembly configuration. Browser clients can select a
// key, not supply factories, launch commands, file paths or provider secrets.
// The empty binding is always available as "none".
type LocalAgents struct {
	Default string
	Choices map[string]AgentFactory
}

type bindingState struct {
	Selection  string `json:"selection"`
	Generation uint64 `json:"generation"`
	Identity   string `json:"identity,omitempty"`
}

type bindingRequest struct {
	Selection  string `json:"selection"`
	Generation uint64 `json:"generation"`
	Reconnect  bool   `json:"reconnect,omitempty"`
}

type bindingStatus struct {
	Module     string   `json:"module"`
	State      string   `json:"state"`
	Selection  string   `json:"selection,omitempty"`
	Generation uint64   `json:"generation,omitempty"`
	Choices    []string `json:"choices,omitempty"`
	Notice     string   `json:"notice,omitempty"`
}

// The serial lock permits one probe at a time without blocking the current
// module. The gate fences the actual cutover against role streams and routes.
// The outgoing supervisor has its own idle barrier for event-driven workers.
type agentBindings struct {
	serial  sync.Mutex
	gate    sync.RWMutex
	deps    AgentDeps
	catalog LocalAgents
	choices []string
	state   bindingState
	notice  string
	current AgentModule
	routes  *http.ServeMux
	root    context.Context
	cancel  context.CancelFunc
}

func newBindings(ctx context.Context, deps AgentDeps, catalog LocalAgents) (*agentBindings, error) {
	if !deps.Config.LocalWorkspace || !deps.Config.MCP.Disabled || !deps.Config.Web.Disabled {
		return nil, errors.New("interactive supervisor switching requires an isolated local workspace")
	}
	b := &agentBindings{deps: deps, catalog: catalog, choices: []string{"none"}, state: bindingState{Selection: catalog.Default, Generation: 1}}
	for _, id := range []string{"legacy", "acp"} {
		if catalog.Choices[id] != nil {
			b.choices = append(b.choices, id)
		}
	}
	if len(catalog.Choices) != len(b.choices)-1 || !b.valid(catalog.Default) {
		return nil, errors.New("invalid interactive supervisor catalog")
	}
	saved, err := deps.Log.Store.Setting(ctx, bindingKey)
	if err == nil {
		if err := json.Unmarshal([]byte(saved), &b.state); err != nil || b.state.Generation == 0 || !b.valid(b.state.Selection) {
			return nil, errors.New("invalid saved supervisor binding")
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	m, mux, err := b.prepare(b.state.Selection)
	if err != nil || b.state.Identity != m.Identity {
		// A saved but missing/invalid profile must not break startup or fall
		// back to a potentially online model. The user can repair or detach.
		b.notice = "上次的主管配置不可用或已更改。重新应用后才会连接，本机工作区仍可使用。"
	} else {
		b.current, b.routes = m, mux
	}
	return b, nil
}

func (b *agentBindings) valid(id string) bool { return id == "none" || b.catalog.Choices[id] != nil }

func (b *agentBindings) prepare(id string) (AgentModule, *http.ServeMux, error) {
	var m AgentModule
	if id != "none" {
		var err error
		m, err = b.catalog.Choices[id](b.deps)
		if err != nil {
			return AgentModule{}, nil, err
		}
		if m.Name != id || m.Start == nil || m.Wait == nil || m.PauseIfIdle == nil || m.Resume == nil {
			return AgentModule{}, nil, errors.New("module does not implement switch lifecycle")
		}
	}
	mux := http.NewServeMux()
	for _, routes := range m.Routes {
		routes(mux, func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, b.deps.Auth.Require(h)) })
	}
	return m, mux, nil
}

func (b *agentBindings) start(ctx context.Context) {
	b.gate.Lock()
	defer b.gate.Unlock()
	b.root = ctx
	b.startCurrent()
}

func (b *agentBindings) startCurrent() {
	if b.current.Start != nil {
		ctx, stop := context.WithCancel(b.root)
		b.cancel = stop
		b.current.Start(ctx)
	} else {
		sysmap.RegisterProbe("supervisor", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
			return sysmap.Status{State: sysmap.Idle, Summary: "未连接主管"}
		}))
	}
}

func (b *agentBindings) stop() {
	b.serial.Lock()
	defer b.serial.Unlock()
	b.gate.Lock()
	defer b.gate.Unlock()
	b.stopCurrent()
}

func (b *agentBindings) stopCurrent() {
	if b.cancel != nil {
		b.cancel()
		b.current.Wait()
		b.cancel = nil
	}
}

func (b *agentBindings) status(ctx context.Context) bindingStatus {
	b.gate.RLock()
	defer b.gate.RUnlock()
	return b.statusLocked(ctx)
}

func (b *agentBindings) statusLocked(ctx context.Context) bindingStatus {
	v := b.current.status(ctx)
	v.Generation, v.Selection, v.Choices, v.Notice = b.state.Generation, b.state.Selection, b.choices, b.notice
	return v
}

func (b *agentBindings) selectAgent(ctx context.Context, request bindingRequest) (bindingStatus, error) {
	next := bindingState{Selection: request.Selection, Generation: request.Generation}
	if !b.valid(next.Selection) || next.Generation == 0 {
		return bindingStatus{}, errBindingUnavailable
	}
	if !b.serial.TryLock() {
		return bindingStatus{}, errBindingBusy
	}
	defer b.serial.Unlock()
	before := b.status(ctx)
	if before.Generation != next.Generation {
		return bindingStatus{}, errBindingStale
	}
	if before.Selection == next.Selection && before.Notice == "" && !(request.Reconnect && next.Selection == "acp") {
		return before, nil
	}
	m, mux, err := b.prepare(next.Selection)
	if err != nil {
		return bindingStatus{}, errBindingUnavailable
	}
	if m.Probe != nil {
		probe, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := m.Probe(probe)
		cancel()
		if err != nil {
			return bindingStatus{}, errBindingUnavailable
		}
	}
	if !b.gate.TryLock() {
		return bindingStatus{}, errBindingBusy
	}
	defer b.gate.Unlock()
	if b.root == nil || b.root.Err() != nil || ctx.Err() != nil {
		return bindingStatus{}, errBindingUnavailable
	}
	if next.Generation == ^uint64(0) {
		return bindingStatus{}, errBindingUnavailable
	}
	if b.current.PauseIfIdle != nil && !b.current.PauseIfIdle() {
		return bindingStatus{}, errBindingBusy
	}
	next.Generation++
	next.Identity = m.Identity
	encoded, _ := json.Marshal(next)
	if err := b.deps.Log.Store.SetSetting(ctx, bindingKey, string(encoded)); err != nil {
		if b.current.Resume != nil {
			b.current.Resume()
		}
		slog.Warn("cannot persist supervisor binding", "error", err)
		return bindingStatus{}, errBindingUnavailable
	}
	// No outgoing workers or role/route calls can start in this window.
	b.stopCurrent()
	b.current, b.routes, b.state, b.notice = m, mux, next, ""
	b.startCurrent()
	return b.statusLocked(ctx), nil
}

func (b *agentBindings) Routes(_ *http.ServeMux, protect func(string, http.HandlerFunc)) {
	protect("PUT /api/v1/supervisor/binding", func(w http.ResponseWriter, r *http.Request) {
		if !b.deps.Auth.RequireFresh(w, r) {
			return
		}
		var change bindingRequest
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		d.DisallowUnknownFields()
		var extra json.RawMessage
		if d.Decode(&change) != nil || !errors.Is(d.Decode(&extra), io.EOF) || !b.valid(change.Selection) || change.Generation == 0 {
			bindingReply(w, 400, map[string]any{"error": map[string]string{"code": "invalid", "message": "请选择本机可用的主管及当前绑定版本"}})
			return
		}
		v, err := b.selectAgent(r.Context(), change)
		if err != nil {
			code := "binding_unavailable"
			if errors.Is(err, errBindingBusy) {
				code = "binding_busy"
			}
			if errors.Is(err, errBindingStale) {
				code = "binding_stale"
			}
			bindingReply(w, 409, map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
			return
		}
		bindingReply(w, 200, v)
	})
}

func bindingReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (b *agentBindings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.gate.RLock()
	defer b.gate.RUnlock()
	if b.routes != nil && b.root != nil && b.root.Err() == nil {
		if _, pattern := b.routes.Handler(r); pattern != "" {
			b.routes.ServeHTTP(w, r)
			return
		}
	}
	bindingReply(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "no such endpoint"}})
}

func (b *agentBindings) module() AgentModule {
	return AgentModule{Start: b.start, Wait: b.stop, Active: func(thread string) bool {
		b.gate.RLock()
		defer b.gate.RUnlock()
		return b.current.Active != nil && b.current.Active(thread)
	}, RoleRefs: func(ctx context.Context) map[string]string {
		b.gate.RLock()
		defer b.gate.RUnlock()
		return b.current.roleRefs(ctx)
	}, Reviewer: b.role("reviewer"), Approver: b.role("approver"), Inspector: b.role("inspector")}
}

func (b *agentBindings) role(name string) task.Model {
	return task.Model{Name: name, Provider: bindingRole{b, name}}
}

type bindingRole struct {
	b    *agentBindings
	name string
}

func (p bindingRole) Name() string { return p.name }
func (p bindingRole) model() task.Model {
	switch p.name {
	case "reviewer":
		return p.b.current.Reviewer
	case "approver":
		return p.b.current.Approver
	default:
		return p.b.current.Inspector
	}
}
func (p bindingRole) Configured(ctx context.Context) bool {
	p.b.gate.RLock()
	defer p.b.gate.RUnlock()
	return provider.Usable(ctx, p.model().Provider)
}
func (p bindingRole) Stream(ctx context.Context, req provider.Request, emit func(provider.Delta)) (provider.Response, error) {
	p.b.gate.RLock()
	defer p.b.gate.RUnlock()
	m := p.model()
	if !provider.Usable(ctx, m.Provider) {
		return provider.Response{}, errBindingDisabled
	}
	req.Model = m.Name
	return m.Provider.Stream(ctx, req, emit)
}

// Package models keeps the model providers and the model of each role
// (supervisor, reviewer, approver, inspector) in the Hub's database, so
// they are added and switched on the page without editing hub.yaml or
// restarting. hub.yaml's providers and roles are imported once, on the
// first start that finds none in the database.
//
// Every role gets a provider.Provider from For that looks up the current
// choice on each call, so a switch takes effect on the next model call.
package models

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
)

// Roles are the roles a model can be chosen for, in the page's order.
var Roles = []string{config.RoleSupervisor, config.RoleReviewer, config.RoleApprover, config.RoleInspector, config.RoleCompactor}

// ErrNotConfigured: no model is chosen for the role (nor the supervisor's
// to fall back on).
var ErrNotConfigured = errors.New("还没有配置模型：在 设置 → 模型 里添加供应商，并给主管选一个模型")

const seededKey = "models.seeded"

type Registry struct {
	log *eventlog.Log

	mu    sync.Mutex
	cache map[string]cached // by provider id
	// Wrap lets the Hub add retries around every provider it builds.
	Wrap func(provider.Provider) provider.Provider
}

type cached struct {
	at int64 // the provider's updated_at it was built from
	p  provider.Provider
}

func New(log *eventlog.Log) *Registry {
	r := &Registry{log: log, cache: map[string]cached{}}
	sysmap.RegisterInstances("provider", r.mapProviders)
	sysmap.RegisterProbe("models", sysmap.ProbeFunc(r.mapStatus))
	return r
}

func (r *Registry) store() *store.Store { return r.log.Store }

// Seed imports hub.yaml's providers and roles the first time the Hub
// starts with this database. Keys stay in the environment (api_key_env).
func (r *Registry) Seed(ctx context.Context, cfg *config.Hub) error {
	if _, err := r.store().Setting(ctx, seededKey); err == nil {
		return nil
	}
	existing, err := r.store().ModelProviders(ctx)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		used := map[string][]string{}
		for _, ref := range cfg.Roles {
			if p, m, ok := strings.Cut(ref, "/"); ok && !contains(used[p], m) {
				used[p] = append(used[p], m)
			}
		}
		for _, pc := range cfg.Providers {
			if err := r.store().PutModelProvider(ctx, store.ModelProvider{
				ID: slug(pc.Name), Name: pc.Name, Kind: pc.Kind, BaseURL: pc.BaseURL, APIKeyEnv: pc.APIKeyEnv, Models: used[pc.Name],
			}); err != nil {
				return err
			}
		}
		roles := map[string]store.ModelRole{}
		for role, ref := range cfg.Roles {
			p, m, _ := strings.Cut(ref, "/")
			// Roles hub.yaml left to fall back on the supervisor stay unset here too.
			if role != config.RoleSupervisor && ref == cfg.Roles[config.RoleSupervisor] && cfg.Efforts[role] == "" && !cfg.RoleSet(role) {
				continue
			}
			roles[role] = store.ModelRole{ProviderID: slug(p), Model: m, Effort: cfg.Efforts[role]}
		}
		if len(roles) > 0 {
			if err := r.store().SetModelRoles(ctx, roles); err != nil {
				return err
			}
		}
		if len(cfg.Providers) > 0 {
			slog.Info("models: imported providers and roles from hub.yaml", "providers", len(cfg.Providers), "roles", len(roles))
		}
	}
	return r.store().SetSetting(ctx, seededKey, time.Now().Format(time.RFC3339))
}

// Resolved is what a role uses right now.
type Resolved struct {
	Provider provider.Provider
	Name     string // the provider's name
	Model    string
	Effort   string
	Role     string // the role whose choice this is (the supervisor's when the role has none)
	// ContextWindow is the model's context window in tokens, 0 when not set.
	ContextWindow int
}

// Resolve finds the role's current model: its own choice, else the
// supervisor's.
func (r *Registry) Resolve(ctx context.Context, role string) (Resolved, error) {
	roles, err := r.store().ModelRoles(ctx)
	if err != nil {
		return Resolved{}, err
	}
	from := role
	choice, ok := roles[role]
	if !ok || choice.ProviderID == "" || choice.Model == "" {
		from = config.RoleSupervisor
		if choice, ok = roles[from]; !ok || choice.ProviderID == "" || choice.Model == "" {
			return Resolved{}, ErrNotConfigured
		}
	}
	p, name, err := r.instance(ctx, choice.ProviderID)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Provider: p, Name: name, Model: choice.Model, Effort: choice.Effort, Role: from, ContextWindow: choice.ContextWindow}, nil
}

// ContextWindow is the context window of the role's current model in
// tokens, 0 when not set.
func (r *Registry) ContextWindow(ctx context.Context, role string) int {
	res, err := r.Resolve(ctx, role)
	if err != nil {
		return 0
	}
	return res.ContextWindow
}

// instance builds (or reuses) the client of a provider.
func (r *Registry) instance(ctx context.Context, id string) (provider.Provider, string, error) {
	mp, err := r.store().ModelProvider(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", fmt.Errorf("模型供应商 %q 已经不存在了，请在 设置 → 模型 里重新选择", id)
	} else if err != nil {
		return nil, "", err
	}
	r.mu.Lock()
	c, ok := r.cache[id]
	r.mu.Unlock()
	if ok && c.at == mp.UpdatedAt {
		return c.p, mp.Name, nil
	}
	key, err := r.key(ctx, mp)
	if err != nil {
		return nil, "", err
	}
	p, err := provider.New(provider.Config{Name: mp.Name, Kind: mp.Kind, BaseURL: baseURL(mp), APIKey: key})
	if err != nil {
		return nil, "", err
	}
	if r.Wrap != nil {
		p = r.Wrap(p)
	}
	r.mu.Lock()
	r.cache[id] = cached{at: mp.UpdatedAt, p: p}
	r.mu.Unlock()
	return p, mp.Name, nil
}

// key is the provider's stored key, else its environment variable's.
func (r *Registry) key(ctx context.Context, mp store.ModelProvider) (string, error) {
	k, err := r.store().ModelKey(ctx, mp.ID)
	if err != nil || k != "" {
		return k, err
	}
	if mp.APIKeyEnv != "" {
		return os.Getenv(mp.APIKeyEnv), nil
	}
	return "", nil
}

func baseURL(mp store.ModelProvider) string {
	if mp.BaseURL != "" {
		return mp.BaseURL
	}
	return provider.DefaultBaseURL(mp.Kind)
}

// For is the role's provider: every call uses the role's model at that
// moment, with its effort.
func (r *Registry) For(role string) provider.Provider { return &roleProvider{r: r, role: role} }

// Configured reports whether a model would answer for the role.
func (r *Registry) Configured(ctx context.Context, role string) bool {
	_, err := r.Resolve(ctx, role)
	return err == nil
}

type roleProvider struct {
	r    *Registry
	role string

	mu   sync.Mutex
	last string // the provider's name of the last call, for Name
}

// Name is the provider of the last call (usage is recorded after one).
func (p *roleProvider) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last != "" {
		return p.last
	}
	if res, err := p.r.Resolve(context.Background(), p.role); err == nil {
		return res.Name
	}
	return p.role
}

func (p *roleProvider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) (provider.Response, error) {
	res, err := p.r.Resolve(ctx, p.role)
	if err != nil {
		return provider.Response{}, err
	}
	p.mu.Lock()
	p.last = res.Name
	p.mu.Unlock()
	req.Model = res.Model
	if req.Effort == "" {
		req.Effort = res.Effort
	}
	start := time.Now()
	resp, err := res.Provider.Stream(ctx, req, onDelta)
	duration := time.Since(start)
	refs := sysmap.RefsFrom(ctx)
	summary := p.role + " · " + res.Model
	source := ""
	switch p.role {
	case "supervisor", "compactor":
		source = "supervisor"
	case "reviewer", "approver":
		source = "task"
	case "inspector":
		source = "inspector"
	}
	if source != "" {
		sysmap.Call(sysmap.EdgeID(source, "models"), refs, summary, duration, err)
	}
	sysmap.Call("models>provider:"+res.Name, refs, summary, duration, err)
	if resp.Model == "" {
		resp.Model = res.Model
	}
	return resp, err
}

// Usable reports whether p will answer: false for nil and for a role
// with no model configured.
func Usable(ctx context.Context, p provider.Provider) bool {
	if p == nil {
		return false
	}
	if rp, ok := p.(*roleProvider); ok {
		return rp.r.Configured(ctx, rp.role)
	}
	return true
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		s = "provider"
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

var lookupEnv = os.LookupEnv

func providerStatus(name string) sysmap.Status {
	s := sysmap.Stats("models>provider:" + name)
	if s.Calls == 0 {
		return sysmap.Status{State: sysmap.Idle, Summary: "最近没有调用"}
	}
	if s.Errors >= 3 {
		return sysmap.Status{State: sysmap.Degraded, Summary: fmt.Sprintf("最近 %d 次里失败 %d 次：%s", s.Calls, s.Errors, s.LastError)}
	}
	return sysmap.Status{State: sysmap.OK, Summary: fmt.Sprintf("平均 %.1f 秒 · 最近 %d 次", s.AvgMS/1000, s.Calls)}
}
func (r *Registry) mapProviders(ctx context.Context) []sysmap.Instance {
	providers, err := r.store().ModelProviders(ctx)
	if err != nil {
		return nil
	}
	out := make([]sysmap.Instance, 0, len(providers))
	for _, p := range providers {
		out = append(out, sysmap.Instance{Module: sysmap.Module{ID: "provider:" + p.Name, Name: p.Name, Layer: sysmap.LayerExternal, Desc: "模型供应商（" + p.Kind + "）"}, Status: providerStatus(p.Name)})
	}
	return out
}
func (r *Registry) mapStatus(ctx context.Context) sysmap.Status {
	instances := r.mapProviders(ctx)
	calls, errors, recent := 0, 0, 0
	state := sysmap.Idle
	var items []sysmap.Item
	for _, p := range instances {
		s := sysmap.Stats("models>" + p.Module.ID)
		calls += s.Calls
		errors += s.Errors
		recent += sysmap.Sum("models>"+p.Module.ID, 60)
		if p.Status.State == sysmap.Degraded {
			state = sysmap.Degraded
		}
		items = append(items, sysmap.Item{ID: p.Module.ID, Label: p.Module.Name, Detail: p.Status.Summary, State: p.Status.State})
	}
	if state != sysmap.Degraded && recent > 0 {
		state = sysmap.Busy
	}
	return sysmap.Status{State: state, Summary: fmt.Sprintf("最近 %d 次调用，失败 %d 次", calls, errors), Items: items}
}

// Fixed chooses one model independently of the current supervisor role.
func (r *Registry) Fixed(providerID, model, effort string) provider.Provider {
	return &fixedProvider{r: r, id: providerID, model: model, effort: effort}
}

type fixedProvider struct {
	r                 *Registry
	id, model, effort string
	mu                sync.Mutex
	name              string
}

func (p *fixedProvider) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.name != "" {
		return p.name
	}
	return p.id
}
func (p *fixedProvider) Stream(ctx context.Context, req provider.Request, delta func(provider.Delta)) (provider.Response, error) {
	impl, name, err := p.r.instance(ctx, p.id)
	if err != nil {
		return provider.Response{}, err
	}
	p.mu.Lock()
	p.name = name
	p.mu.Unlock()
	req.Model = p.model
	if req.Effort == "" {
		req.Effort = p.effort
	}
	resp, err := impl.Stream(ctx, req, delta)
	if resp.Model == "" {
		resp.Model = p.model
	}
	return resp, err
}

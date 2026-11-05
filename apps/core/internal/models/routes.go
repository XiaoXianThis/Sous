package models

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// ModelsChanged tells pages to reload the model settings. It never carries
// keys.
const ModelsChanged event.Type = "models.changed"

type changedPayload struct {
	What string `json:"what"` // provider:<id> / provider_deleted:<id> / roles
}

// Fresh is the Hub's recent-login check (answers 401 "reauth" itself).
// Changing providers needs it: a provider's base URL decides where its API
// key is sent, so a stolen cookie must not be enough to redirect it.
type Fresh func(http.ResponseWriter, *http.Request) bool

// Routes serves /api/v1/models.
func (r *Registry) Routes(fresh Fresh) func(*http.ServeMux, func(string, http.HandlerFunc)) {
	return func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		protect("GET /api/v1/models", r.get)
		protect("POST /api/v1/models/providers", r.guard(fresh, r.putProvider))
		protect("PATCH /api/v1/models/providers/{id}", r.guard(fresh, r.putProvider))
		protect("DELETE /api/v1/models/providers/{id}", r.guard(fresh, r.deleteProvider))
		protect("POST /api/v1/models/providers/{id}/discover", r.discover)
		protect("POST /api/v1/models/test", r.test)
		protect("PUT /api/v1/models/roles", r.putRoles)
	}
}

func (r *Registry) guard(fresh Fresh, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if fresh != nil && !fresh(w, req) {
			return
		}
		h(w, req)
	}
}

type providerView struct {
	store.ModelProvider
	// KeySet: a key is stored on the Hub. EnvSet: api_key_env names a
	// variable that is set. The key itself is never returned.
	KeySet     bool   `json:"key_set"`
	EnvSet     bool   `json:"env_set"`
	DefaultURL string `json:"default_url"`
}

type roleView struct {
	store.ModelRole
	Inherited bool `json:"inherited"` // no choice of its own: uses the supervisor's
}

func (r *Registry) view(ctx context.Context) (map[string]any, error) {
	ps, err := r.store().ModelProviders(ctx)
	if err != nil {
		return nil, err
	}
	views := []providerView{}
	for _, p := range ps {
		k, _ := r.store().ModelKey(ctx, p.ID)
		views = append(views, providerView{ModelProvider: p, KeySet: k != "", EnvSet: p.APIKeyEnv != "" && envSet(p.APIKeyEnv),
			DefaultURL: provider.DefaultBaseURL(p.Kind)})
	}
	set, err := r.store().ModelRoles(ctx)
	if err != nil {
		return nil, err
	}
	roles := map[string]roleView{}
	for _, role := range Roles {
		c, ok := set[role]
		if !ok || c.Model == "" {
			roles[role] = roleView{ModelRole: set["supervisor"], Inherited: role != "supervisor"}
			if role == "supervisor" {
				roles[role] = roleView{}
			}
			continue
		}
		roles[role] = roleView{ModelRole: c}
	}
	kinds := []string{}
	for _, k := range provider.Kinds() {
		kinds = append(kinds, k)
	}
	return map[string]any{"providers": views, "roles": roles, "role_order": Roles, "kinds": kinds, "efforts": provider.Efforts}, nil
}

func (r *Registry) get(w http.ResponseWriter, req *http.Request) {
	v, err := r.view(req.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (r *Registry) changed(ctx context.Context, what string) {
	r.log.Append(ctx, event.New(ModelsChanged, event.ActorUser, "", changedPayload{What: what}))
}

// putProvider creates (POST) or updates (PATCH, only the fields present)
// a provider. api_key: a new key; "" with clear_key removes it.
func (r *Registry) putProvider(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Name      *string   `json:"name"`
		Kind      *string   `json:"kind"`
		BaseURL   *string   `json:"base_url"`
		APIKeyEnv *string   `json:"api_key_env"`
		APIKey    *string   `json:"api_key"`
		ClearKey  bool      `json:"clear_key"`
		Models    *[]string `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	ctx := req.Context()
	var p store.ModelProvider
	if id := req.PathValue("id"); id != "" {
		var err error
		if p, err = r.store().ModelProvider(ctx, id); err != nil {
			writeError(w, http.StatusNotFound, "not_found", "供应商不存在")
			return
		}
	} else {
		p.ID = ids.New()
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	set(&p.Name, b.Name)
	set(&p.Kind, b.Kind)
	set(&p.BaseURL, b.BaseURL)
	set(&p.APIKeyEnv, b.APIKeyEnv)
	if b.Models != nil {
		p.Models = cleanModels(*b.Models)
	}
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
	if p.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "需要名字")
		return
	}
	if !slices.Contains(provider.Kinds(), p.Kind) {
		writeError(w, http.StatusBadRequest, "invalid", "不认识的协议："+p.Kind)
		return
	}
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		local := err == nil && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && local)) {
			// The API key goes to this address: only over TLS, except to this machine.
			writeError(w, http.StatusBadRequest, "invalid", "地址要以 https:// 开头（本机的服务可以用 http://localhost）")
			return
		}
	}
	if err := r.store().PutModelProvider(ctx, p); err != nil {
		internal(w, err)
		return
	}
	if b.APIKey != nil && strings.TrimSpace(*b.APIKey) != "" {
		if err := r.store().SetModelKey(ctx, p.ID, strings.TrimSpace(*b.APIKey)); err != nil {
			internal(w, err)
			return
		}
	} else if b.ClearKey {
		r.store().SetModelKey(ctx, p.ID, "")
	}
	slog.Info("models: provider saved", "id", p.ID, "name", p.Name, "kind", p.Kind)
	r.changed(ctx, "provider:"+p.ID)
	r.get(w, req)
}

func (r *Registry) deleteProvider(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	id := req.PathValue("id")
	roles, _ := r.store().ModelRoles(ctx)
	for role, c := range roles {
		if c.ProviderID == id {
			writeError(w, http.StatusConflict, "in_use", "还有角色在用这个供应商（"+role+"），先给它换一个模型")
			return
		}
	}
	if err := r.store().DeleteModelProvider(ctx, id); err != nil {
		internal(w, err)
		return
	}
	r.mu.Lock()
	delete(r.cache, id)
	r.mu.Unlock()
	r.changed(ctx, "provider_deleted:"+id)
	r.get(w, req)
}

// discover asks the provider which models it offers (not saved).
func (r *Registry) discover(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	mp, err := r.store().ModelProvider(ctx, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "供应商不存在")
		return
	}
	key, _ := r.key(ctx, mp)
	list, err := provider.ListModels(ctx, provider.Config{Kind: mp.Kind, BaseURL: baseURL(mp), APIKey: key})
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider", "读取模型列表失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": list})
}

// test sends one short message to a provider's model and reports the
// answer and how long it took.
func (r *Registry) test(w http.ResponseWriter, req *http.Request) {
	var b struct {
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
		Effort     string `json:"effort"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 64<<10)).Decode(&b); err != nil || b.ProviderID == "" || b.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid", "需要供应商和模型")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 90*time.Second)
	defer cancel()
	p, _, err := r.instance(ctx, b.ProviderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	start := time.Now()
	resp, err := p.Stream(ctx, provider.Request{Model: b.Model, Effort: b.Effort, MaxTokens: 64,
		Messages: []provider.Message{provider.Text(provider.RoleUser, "Reply with the single word: ok")}}, nil)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "ms": ms})
		return
	}
	text := resp.Message.PlainText()
	if len([]rune(text)) > 200 {
		text = string([]rune(text)[:200])
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reply": text, "ms": ms, "usage": resp.Usage})
}

// roleInput is one role in PUT /models/roles. Without context_window the
// role keeps its window while its model stays the same.
type roleInput struct {
	ProviderID    string `json:"provider_id"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	ContextWindow *int   `json:"context_window"`
}

// putRoles sets the model of the roles present: {"supervisor":
// {provider_id, model, effort, context_window}}; an empty model clears a
// role's own choice (it falls back on the supervisor's).
func (r *Registry) putRoles(w http.ResponseWriter, req *http.Request) {
	var b map[string]*roleInput
	if err := json.NewDecoder(io.LimitReader(req.Body, 64<<10)).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	ctx := req.Context()
	roles, err := r.store().ModelRoles(ctx)
	if err != nil {
		internal(w, err)
		return
	}
	for role, in := range b {
		if !slices.Contains(Roles, role) {
			writeError(w, http.StatusBadRequest, "invalid", "不认识的角色："+role)
			return
		}
		if in == nil || in.Model == "" {
			if role == "supervisor" {
				writeError(w, http.StatusBadRequest, "invalid", "主管必须有一个模型")
				return
			}
			delete(roles, role)
			continue
		}
		c := &store.ModelRole{ProviderID: in.ProviderID, Model: strings.TrimSpace(in.Model), Effort: in.Effort}
		if in.ContextWindow != nil {
			c.ContextWindow = *in.ContextWindow
		} else if old, ok := roles[role]; ok && old.ProviderID == c.ProviderID && old.Model == c.Model {
			c.ContextWindow = old.ContextWindow
		}
		if _, err := r.store().ModelProvider(ctx, c.ProviderID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "供应商不存在")
			return
		}
		if c.Effort != "" && !slices.Contains(provider.Efforts, c.Effort) {
			writeError(w, http.StatusBadRequest, "invalid", "思考强度只能是 "+strings.Join(provider.Efforts, " / "))
			return
		}
		if c.ContextWindow != 0 && (c.ContextWindow < 8000 || c.ContextWindow > 10_000_000) {
			writeError(w, http.StatusBadRequest, "invalid", "上下文窗口要在 8000 到 10000000 token 之间（不填用默认的 128000）")
			return
		}
		roles[role] = *c
	}
	if err := r.store().SetModelRoles(ctx, roles); err != nil {
		internal(w, err)
		return
	}
	r.changed(ctx, "roles")
	r.get(w, req)
}

// RoleRefs is each role's current "provider/model" (with effort), for
// places that only show it.
func (r *Registry) RoleRefs(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, role := range Roles {
		if res, err := r.Resolve(ctx, role); err == nil {
			ref := res.Name + "/" + res.Model
			if res.Effort != "" {
				ref += " · " + res.Effort
			}
			out[role] = ref
		}
	}
	return out
}

func cleanModels(in []string) []string {
	out := []string{}
	for _, m := range in {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

func envSet(name string) bool {
	v, ok := lookupEnv(name)
	return ok && v != ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func internal(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Error("models: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

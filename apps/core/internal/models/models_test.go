package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(eventlog.New(st, event.NewBus()))
}

// fakeOpenAI answers /models and /chat/completions, recording the model and
// key of each chat request.
func fakeOpenAI(t *testing.T, got *[]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			if r.Header.Get("Authorization") != "Bearer k1" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"data":[{"id":"b-model"},{"id":"a-model"}]}`)
		case "/chat/completions":
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &body)
			*got = append(*got, fmt.Sprintf("%v|%v|%s", body["model"], body["reasoning_effort"], r.Header.Get("Authorization")))
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSeedFromConfigOnce(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	cfg := &config.Hub{
		Providers: []config.Provider{{Name: "DeepSeek", Kind: "openai-chat", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DS_KEY"}},
		Roles:     map[string]string{"supervisor": "DeepSeek/pro", "inspector": "DeepSeek/flash"},
		Efforts:   map[string]string{"supervisor": "high"},
	}
	if err := r.Seed(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	ps, _ := r.store().ModelProviders(ctx)
	if len(ps) != 1 || ps[0].ID != "deepseek" || ps[0].APIKeyEnv != "DS_KEY" || strings.Join(ps[0].Models, ",") != "flash,pro" && strings.Join(ps[0].Models, ",") != "pro,flash" {
		t.Fatalf("providers %+v", ps)
	}
	roles, _ := r.store().ModelRoles(ctx)
	if roles["supervisor"] != (store.ModelRole{ProviderID: "deepseek", Model: "pro", Effort: "high"}) || roles["inspector"].Model != "flash" {
		t.Fatalf("roles %+v", roles)
	}
	// Later starts leave the page's configuration alone.
	r.store().SetModelRoles(ctx, map[string]store.ModelRole{"supervisor": {ProviderID: "deepseek", Model: "other"}})
	r.Seed(ctx, cfg)
	if roles, _ = r.store().ModelRoles(ctx); roles["supervisor"].Model != "other" {
		t.Fatalf("seed ran twice: %+v", roles)
	}
}

func TestRoleSwitchesLiveAndFallsBack(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	var got []string
	srv := fakeOpenAI(t, &got)
	r.store().PutModelProvider(ctx, store.ModelProvider{ID: "p", Name: "P", Kind: "openai-chat", BaseURL: srv.URL, Models: []string{"m1", "m2"}})
	r.store().SetModelKey(ctx, "p", "k1")

	rev := r.For(config.RoleReviewer)
	if Usable(ctx, rev) {
		t.Fatal("usable with nothing configured")
	}
	if _, err := rev.Stream(ctx, provider.Request{Messages: []provider.Message{provider.Text(provider.RoleUser, "hi")}}, nil); err != ErrNotConfigured {
		t.Fatalf("err %v", err)
	}
	r.store().SetModelRoles(ctx, map[string]store.ModelRole{"supervisor": {ProviderID: "p", Model: "m1", Effort: "low"}})
	ask := func(p provider.Provider) provider.Response {
		resp, err := p.Stream(ctx, provider.Request{Model: "stale", Messages: []provider.Message{provider.Text(provider.RoleUser, "hi")}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// The reviewer has no model of its own: it uses the supervisor's.
	if resp := ask(rev); resp.Model != "m1" || got[0] != "m1|low|Bearer k1" || rev.Name() != "P" {
		t.Fatalf("%+v %v", resp, got)
	}
	// A switch applies to the next call, without rebuilding anything.
	r.store().SetModelRoles(ctx, map[string]store.ModelRole{"supervisor": {ProviderID: "p", Model: "m1"}, "reviewer": {ProviderID: "p", Model: "m2", Effort: "max"}})
	ask(rev)
	if got[1] != "m2|xhigh|Bearer k1" {
		t.Fatalf("after switch: %v", got)
	}
	// A new key is used at once too.
	r.store().SetModelKey(ctx, "p", "k2")
	ask(rev)
	if !strings.HasSuffix(got[2], "Bearer k2") {
		t.Fatalf("after key change: %v", got)
	}
}

func TestRoutes(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	var got []string
	srv := fakeOpenAI(t, &got)
	fresh := true
	mux := http.NewServeMux()
	r.Routes(func(w http.ResponseWriter, _ *http.Request) bool {
		if !fresh {
			writeError(w, 401, "reauth", "again")
		}
		return fresh
	})(mux, func(p string, h http.HandlerFunc) { mux.HandleFunc(p, h) })
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	call := func(method, path, body string, out any) int {
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(raw), "k1") {
			t.Fatalf("%s %s leaked the key: %s", method, path, raw)
		}
		if out != nil {
			json.Unmarshal(raw, out)
		}
		return resp.StatusCode
	}

	// Keys go only over https (or to this machine).
	if code := call("POST", "/api/v1/models/providers", `{"name":"x","kind":"openai-chat","base_url":"http://evil.example"}`, nil); code != 400 {
		t.Fatalf("plain http to another host: %d", code)
	}
	fresh = false
	if code := call("POST", "/api/v1/models/providers", `{"name":"P","kind":"openai-chat","base_url":"`+srv.URL+`","api_key":"k1"}`, nil); code != 401 {
		t.Fatalf("without a fresh login: %d", code)
	}
	fresh = true
	var v struct {
		Providers []struct {
			ID     string   `json:"id"`
			KeySet bool     `json:"key_set"`
			Models []string `json:"models"`
		}
		Roles map[string]roleView
	}
	if code := call("POST", "/api/v1/models/providers", `{"name":"P","kind":"openai-chat","base_url":"`+srv.URL+`","api_key":"k1","models":["m1"]}`, &v); code != 200 || len(v.Providers) != 1 || !v.Providers[0].KeySet {
		t.Fatalf("create %d %+v", code, v)
	}
	id := v.Providers[0].ID
	var d struct{ Models []string }
	if code := call("POST", "/api/v1/models/providers/"+id+"/discover", "", &d); code != 200 || strings.Join(d.Models, ",") != "a-model,b-model" {
		t.Fatalf("discover %d %+v", code, d)
	}
	var tr struct {
		OK    bool
		Reply string
	}
	if call("POST", "/api/v1/models/test", `{"provider_id":"`+id+`","model":"m1"}`, &tr); !tr.OK || tr.Reply != "ok" {
		t.Fatalf("test %+v", tr)
	}
	if code := call("PUT", "/api/v1/models/roles", `{"supervisor":{"provider_id":"`+id+`","model":"m1","effort":"ultra"}}`, nil); code != 400 {
		t.Fatalf("bad effort: %d", code)
	}
	if code := call("PUT", "/api/v1/models/roles", `{"supervisor":{"provider_id":"`+id+`","model":"m1","effort":"high"}}`, &v); code != 200 || v.Roles["supervisor"].Model != "m1" || !v.Roles["reviewer"].Inherited {
		t.Fatalf("roles %d %+v", code, v.Roles)
	}
	// The context window stays while the model does, unless it is set.
	if code := call("PUT", "/api/v1/models/roles", `{"supervisor":{"provider_id":"`+id+`","model":"m1","context_window":64000}}`, &v); code != 200 || v.Roles["supervisor"].ContextWindow != 64000 {
		t.Fatalf("window %d %+v", code, v.Roles)
	}
	if code := call("PUT", "/api/v1/models/roles", `{"supervisor":{"provider_id":"`+id+`","model":"m1","effort":"low"}}`, &v); code != 200 || v.Roles["supervisor"].ContextWindow != 64000 {
		t.Fatalf("window kept %d %+v", code, v.Roles)
	}
	if r.ContextWindow(ctx, "compactor") != 64000 {
		t.Fatal("the compactor should fall back on the supervisor's window")
	}
	if code := call("PUT", "/api/v1/models/roles", `{"supervisor":{"provider_id":"`+id+`","model":"m1","context_window":100}}`, nil); code != 400 {
		t.Fatalf("tiny window: %d", code)
	}
	if code := call("DELETE", "/api/v1/models/providers/"+id, "", nil); code != 409 {
		t.Fatalf("delete in use: %d", code)
	}
	if !r.Configured(ctx, "inspector") {
		t.Fatal("inspector should fall back on the supervisor")
	}
}

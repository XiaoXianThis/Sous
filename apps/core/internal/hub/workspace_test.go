package hub_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/legacyagent"
)

func TestLocalWorkspaceWithoutSupervisor(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.ForLocalWorkspace(dir)
	// Deliberately provide previous model config. Without the optional module
	// it must not be imported or dialed.
	cfg.Providers = []config.Provider{{Name: "old", Kind: "mock"}}
	cfg.Roles[config.RoleSupervisor] = "old/echo"
	h, err := hub.Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if _, err := h.Log.EnsureMainThread(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Tasks.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Auth.NewSetupToken(ctx); err != nil {
		t.Fatal(err)
	}
	if h.Tasks.Approver.Provider != nil || h.Tasks.Reviewer.Provider != nil || h.Inspector.Provider != nil {
		t.Fatal("agentless workspace unexpectedly assembled model providers")
	}
	if _, err := os.Stat(filepath.Join(dir, "hub.db")); err != nil {
		t.Fatal(err)
	}
	const token, csrf = "test-session", "test-csrf"
	hash := sha256.Sum256([]byte(token))
	if err := h.Store.CreateSession(ctx, hex.EncodeToString(hash[:]), csrf, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, host, origin string, cookies bool, csrfToken string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, cfg.PublicURL+path, strings.NewReader(body))
		if host != "" {
			r.Host = host
		}
		if cookies {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		}
		r.Header.Set(auth.CSRFHeader, csrfToken)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.HTTP.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	me := call("GET", "/api/v1/me", "", "", "", false, "", 200)
	if !strings.Contains(me.Body.String(), `"local_workspace":true`) || me.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected discovery: %v %s", me.Header(), me.Body)
	}
	call("GET", "/api/v1/capabilities", "", "", "", false, "", 401)
	call("GET", "/api/v1/threads", "", "example.test:8787", "", true, "", 421)
	call("POST", "/api/v1/auth/passkey/register/begin", "{}", "", "http://localhost:9999", false, "", 403)
	call("POST", "/api/v1/auth/dev-login", "{}", "", "", false, "", 404)
	call("POST", "/api/v1/auth/desktop", "{}", "", "", false, "", 404)
	call("GET", "/node", "", "", "", false, "", 404)
	call("POST", "/api/v1/node/pair", "{}", "", "", false, "", 404)
	call("POST", "/api/v1/nodes/pairing-code", "{}", "", "", true, csrf, 404)
	call("POST", "/api/v1/tasks", `{"title":"Review"}`, "", "", true, "", 403)
	c := call("GET", "/api/v1/capabilities", "", "", "", true, "", 200)
	if !strings.Contains(c.Body.String(), `"state":"unbound"`) || !strings.Contains(c.Body.String(), `"node_pairing":false`) {
		t.Fatalf("unexpected capabilities: %s", c.Body)
	}
	main, err := h.Store.MainThreadID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	msgs := "/api/v1/threads/" + main + "/messages"
	call("POST", msgs, `{"content":"keep this"}`, "", cfg.PublicURL, true, csrf, 202)
	list := call("GET", msgs, "", "", "", true, "", 200)
	if !strings.Contains(list.Body.String(), `"content":"keep this"`) || strings.Contains(list.Body.String(), `"role":"assistant"`) {
		t.Fatalf("message must persist without an invented reply: %s", list.Body)
	}
	call("POST", "/api/v1/tasks", `{"title":"Review","kind":"general"}`, "", cfg.PublicURL, true, csrf, 201)
	call("GET", "/api/v1/settings", "", "", "", true, "", 200)
	call("POST", "/api/v1/projects", `{"name":"Planning"}`, "", cfg.PublicURL, true, csrf, 201)
	memory := call("POST", "/api/v1/memories", `{"kind":"note","content":"local preference"}`, "", cfg.PublicURL, true, csrf, 201)
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(memory.Body.Bytes(), &saved); err != nil || saved.ID == "" {
		t.Fatalf("cannot read saved memory: %s", memory.Body)
	}
	call("PUT", "/api/v1/memories/"+saved.ID, `{"kind":"note","content":"updated locally"}`, "", cfg.PublicURL, true, csrf, 200)
	call("GET", "/api/v1/updates", "", "", "", true, "", 404)
	call("POST", "/api/v1/push/test", "{}", "", cfg.PublicURL, true, csrf, 404)
	call("POST", "/api/v1/auth/logout", "{}", "", cfg.PublicURL, true, "", 403)
	call("POST", "/api/v1/auth/logout", "{}", "", cfg.PublicURL, true, csrf, 200)
	call("GET", msgs, "", "", "", true, "", 401)
}

func TestLocalWorkspaceExclusivityAndRelease(t *testing.T) {
	cfg := config.ForLocalWorkspace(t.TempDir())
	h, err := hub.Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := hub.Open(cfg, nil); err == nil {
		_ = other.Close()
		t.Error("second Hub opened the same workspace")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	broken := errors.New("invalid module")
	if _, err := hub.Open(cfg, func(hub.AgentDeps) (hub.AgentModule, error) { return hub.AgentModule{}, broken }); !errors.Is(err, broken) {
		t.Fatalf("expected startup failure, got %v", err)
	}
	h, err = hub.Open(cfg, nil)
	if err != nil {
		t.Fatalf("startup failure leaked the lock: %v", err)
	}
	defer h.Close()
}

func TestModuleLifecycleAndLegacyUpgrade(t *testing.T) {
	cfg := config.ForLocalWorkspace(t.TempDir())
	cfg.Providers = []config.Provider{{Name: "mock", Kind: "mock"}}
	cfg.Roles[config.RoleSupervisor] = "mock/echo"
	var starts, waits atomic.Int32
	factory := func(d hub.AgentDeps) (hub.AgentModule, error) {
		m, err := legacyagent.New(d)
		start, wait := m.Start, m.Wait
		m.Start = func(ctx context.Context) { starts.Add(1); start(ctx) }
		m.Wait = func() { wait(); waits.Add(1) }
		return m, err
	}
	h, err := hub.Open(cfg, factory)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = h.Close()
		t.Fatal(err)
	}
	h.Started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.RunOn(ctx, ln) }()
	select {
	case <-h.Started:
	case err := <-done:
		t.Fatalf("startup failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timeout")
	}
	mainID, err := h.Store.MainThreadID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown timeout")
	}
	if starts.Load() != 1 || waits.Load() != 1 {
		t.Fatalf("module lifecycle %d/%d", starts.Load(), waits.Load())
	}
	reopened, err := hub.Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if id, err := reopened.Store.MainThreadID(context.Background()); id != mainID || err != nil {
		t.Fatalf("removing a module changed the workspace identity: %q %v", id, err)
	}
}

func TestSandboxDispatchHTTPIsBrowserRequestOnly(t *testing.T) {
	ctx := context.Background()
	cfg := config.ForLocalWorkspace(t.TempDir())
	cfg.LocalSandboxDispatch = true
	h, err := hub.Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Tasks.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	session, csrf := "test-owner", "csrf-data"
	key := sha256.Sum256([]byte(session))
	if err := h.Store.CreateSession(ctx, hex.EncodeToString(key[:]), csrf, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, cookie, withCSRF bool, origin string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, cfg.PublicURL+path, strings.NewReader(body))
		if cookie {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: session})
		}
		if withCSRF {
			r.Header.Set(auth.CSRFHeader, csrf)
		}
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.HTTP.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d (%s), want %d", method, path, w.Code, w.Body, want)
		}
		return w.Body.String()
	}
	var x struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(call("POST", "/api/v1/tasks", `{"title":"draft","detail":"private task instructions"}`, true, true, cfg.PublicURL, 201)), &x); err != nil || x.ID == "" {
		t.Fatal(err)
	}
	path := "/api/v1/tasks/" + x.ID + "/sandbox-dispatch"
	call("POST", path, `{}`, false, false, cfg.PublicURL, 401)
	call("POST", path, `{}`, true, false, cfg.PublicURL, 403)
	call("POST", path, `{}`, true, true, "http://localhost:4000", 403)
	call("POST", path, `{"path":"/private/foo","command":"ls"}`, true, true, cfg.PublicURL, 400)
	body := call("POST", path, `{}`, true, true, cfg.PublicURL, 202)
	if strings.Contains(body, "private task instructions") || !strings.Contains(body, `"status":"pending"`) {
		t.Fatalf("browser reply leaked task note or approved execution: %s", body)
	}
	call("POST", "/api/v1/sandbox-dispatch/any/claim", `{}`, true, true, cfg.PublicURL, 404)
	call("GET", "/node", "", false, false, "", 404)
}

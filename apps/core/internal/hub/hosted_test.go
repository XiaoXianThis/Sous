package hub

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func hostedFixture(t *testing.T) *config.Hub {
	t.Helper()
	cfg := config.ForLocalWorkspace(filepath.Join(t.TempDir(), "data"))
	cfg.LocalWorkspace, cfg.HostedWorkspace = false, true
	cfg.WebDir = t.TempDir()
	cfg.PublicURL = "https://work.sous.test"
	if err := os.WriteFile(filepath.Join(cfg.WebDir, "index.html"), []byte(`<html><head><meta name="sous-workspace" content="relay" /></head><body>Hello</body></html>`), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHostedWorkspaceFencesAndKeepsSingleAuthority(t *testing.T) {
	cfg := hostedFixture(t)
	h, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = h.Close()
		}
	}()
	if duplicate, err := Open(cfg, nil); err == nil {
		_ = duplicate.Close()
		t.Fatal("opened a second authority")
	}
	ctx := context.Background()
	if _, err := h.Log.EnsureMainThread(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Tasks.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	link, err := h.CreateHostedSetup(ctx)
	if err != nil || !strings.HasPrefix(link, cfg.PublicURL+"/setup#token=") {
		t.Fatal("initial setup did not use a fragment", err)
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte("session-fixture")))
	if err := h.Store.CreateSession(ctx, key, "csrf-fixture", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.WebDir, "private.yaml"), []byte("not for public delivery"), 0o600); err != nil {
		t.Fatal(err)
	}
	call := func(method, origin, base, path, body string, session bool, expect int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		if session {
			r.AddCookie(&http.Cookie{Name: "__Host-sous_session", Value: "session-fixture"})
			r.Header.Set("X-CSRF-Token", "csrf-fixture")
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.HTTP.ServeHTTP(w, r)
		if w.Code != expect {
			t.Fatalf("%s %s: %d, wanted %d: %s", method, path, w.Code, expect, w.Body)
		}
		return w
	}
	if page := call("GET", "", cfg.PublicURL, "/setup", "", false, 200); !strings.Contains(page.Body.String(), `content="hosted"`) || strings.Contains(page.Body.String(), link) {
		t.Fatal("wrong shell or embedded credential")
	}
	if me := call("GET", "", cfg.PublicURL, "/api/v1/me", "", false, 200); !strings.Contains(me.Body.String(), `"hosted_workspace":true`) || me.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unexpected discovery response")
	}
	call("GET", "", "https://other.sous.test", "/api/v1/me", "", false, 421)
	call("GET", "", cfg.PublicURL, "/private.yaml", "", false, 404)
	call("GET", "", cfg.PublicURL, "/api/v1/tasks", "", false, 401)
	call("POST", "", cfg.PublicURL, "/api/v1/tasks", "{}", true, 403)
	call("POST", "https://other.sous.test", cfg.PublicURL, "/api/v1/tasks", "{}", true, 403)
	call("POST", cfg.PublicURL, cfg.PublicURL, "/api/v1/tasks", `{"title":"Host not needed","kind":"general"}`, true, 201)
	call("POST", cfg.PublicURL, cfg.PublicURL, "/api/v1/tasks", `{"title":"No implicit queue","kind":"general","dispatch":true}`, true, 409)
	for _, path := range []string{"/node", "/api/v1/nodes/id/jobs/job", "/api/v1/device/threads", "/api/v1/projects/id/file"} {
		call("GET", cfg.PublicURL, cfg.PublicURL, path, "", true, 404)
	}
	for _, path := range []string{"/api/v1/node/pair", "/api/v1/nodes/pairing-code", "/api/v1/runs", "/api/v1/projects/id/shells", "/api/v1/auth/dev-login", "/api/v1/auth/desktop"} {
		call("POST", cfg.PublicURL, cfg.PublicURL, path, "{}", true, 404)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	other := *cfg
	other.PublicURL = "https://other.sous.test"
	if wrong, err := Open(&other, nil); err == nil {
		_ = wrong.Close()
		t.Fatal("could reuse hosted DB under another origin")
	}
	local := config.ForLocalWorkspace(cfg.DataDir)
	if wrong, err := Open(local, nil); err == nil {
		_ = wrong.Close()
		t.Fatal("could reuse hosted DB as local")
	}
	again, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if tasks, err := again.Store.Tasks(ctx, store.TaskFilter{}); err != nil || len(tasks) != 1 || tasks[0].Title != "Host not needed" {
		t.Fatalf("staging changed durable task state: %+v %v", tasks, err)
	}
}

func TestHostedCannotAdoptPreviouslyCreatedDatabase(t *testing.T) {
	cfg := hostedFixture(t)
	st, err := store.Open(DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if legacy, err := Open(cfg, nil); err == nil {
		_ = legacy.Close()
		t.Fatal("imported an unmarked database into hosted workspace")
	}
}

func TestHostedWorkerAndOwnerAuthoritiesDoNotMix(t *testing.T) {
	cfg := hostedFixture(t)
	cfg.CloudWorkerToken = strings.Repeat("c", 64)
	h, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx := context.Background()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte("session-fixture")))
	if err := h.Store.CreateSession(ctx, key, "csrf-fixture", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	call := func(path, origin, secret string, owner bool, expect int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, cfg.PublicURL+path, strings.NewReader(`{"workload":"microsandbox-shell-v1"}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if secret != "" {
			r.Header.Set("Authorization", "Bearer "+secret)
		}
		if owner {
			r.AddCookie(&http.Cookie{Name: "__Host-sous_session", Value: "session-fixture"})
			r.Header.Set("X-CSRF-Token", "csrf-fixture")
		}
		w := httptest.NewRecorder()
		h.HTTP.ServeHTTP(w, r)
		if w.Code != expect {
			t.Fatalf("%s returned %d (wanted %d): %s", path, w.Code, expect, w.Body)
		}
	}
	const claim = "/api/v1/cloud-worker/claim"
	call(claim, "", "", false, 404)
	call(claim, cfg.PublicURL, "", true, 404)
	call(claim, cfg.PublicURL, cfg.CloudWorkerToken, false, 404)
	call(claim, "", cfg.CloudWorkerToken, true, 404)
	call(claim, "", cfg.CloudWorkerToken, false, 200)
	call("/api/v1/tasks/fixture/cloud-work", cfg.PublicURL, cfg.CloudWorkerToken, false, 401)
	call("/api/v1/tasks/fixture/cloud-work", "", cfg.CloudWorkerToken, false, 403)
}

package hub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
)

func TestDesktopWorkspaceLogin(t *testing.T) {
	cfg := config.ForLocalWorkspace(t.TempDir())
	cfg.LocalDesktopAuth = true
	h, err := hub.Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	const redeem = "/api/v1/auth/desktop"
	request := func(method, path, origin, grant string, cookies []*http.Cookie, csrf string) *http.Request {
		var data bytes.Buffer
		if grant != "" {
			_ = json.NewEncoder(&data).Encode(map[string]string{"grant": grant})
		}
		r := httptest.NewRequest(method, cfg.PublicURL+path, &data)
		r.RemoteAddr = "127.0.0.1:57921"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(auth.CSRFHeader, csrf)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	call := func(r *http.Request, want int) *http.Response {
		t.Helper()
		w := httptest.NewRecorder()
		h.HTTP.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d != %d: %s", r.Method, r.URL.Path, w.Code, want, w.Body)
		}
		return w.Result()
	}
	call(request("GET", "/api/v1/capabilities", "", "", nil, ""), 401)
	call(request("POST", "/api/v1/auth/dev-login", cfg.PublicURL, "", nil, ""), 404)
	call(request("POST", redeem, cfg.PublicURL, strings.Repeat("A", 43), nil, ""), 403)

	grant, err := h.Auth.DesktopGrant(false)
	if err != nil {
		t.Fatal(err)
	}
	call(request("POST", redeem, "", grant, nil, ""), 403)
	call(request("POST", redeem, "http://localhost:9999", grant, nil, ""), 403)
	r := request("POST", redeem, cfg.PublicURL, grant, nil, "")
	r.Header.Set("X-Forwarded-For", "192.0.2.3")
	call(r, 403)
	res := call(request("POST", redeem, cfg.PublicURL, grant, nil, ""), 200)
	cookies := res.Cookies()
	if len(cookies) != 2 || !cookies[0].HttpOnly || cookies[1].HttpOnly || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected session response: %v", res.Header)
	}
	call(request("POST", redeem, cfg.PublicURL, grant, nil, ""), 403)
	call(request("GET", "/api/v1/capabilities", "", "", cookies, ""), 200)
	call(request("POST", "/api/v1/auth/logout", cfg.PublicURL, "", cookies, ""), 403)
	fresh := httptest.NewRecorder()
	if h.Auth.RequireFresh(fresh, request("PUT", "/api/v1/supervisor/binding", cfg.PublicURL, "", cookies, cookies[1].Value)) || fresh.Code != 401 {
		t.Fatal("debug session must not authorize a sensitive operation")
	}
	if has, err := h.Auth.HasPasskey(context.Background()); err != nil || has {
		t.Fatalf("debug login altered credentials: %v %v", has, err)
	}
	call(request("POST", "/api/v1/auth/logout", cfg.PublicURL, "", cookies, cookies[1].Value), 200)
	call(request("GET", "/api/v1/capabilities", "", "", cookies, ""), 401)

	// System re-authentication refreshes the same session (and can leave the
	// current event stream open) rather than replacing the session cookie.
	grant, _ = h.Auth.DesktopGrant(true)
	cookies = call(request("POST", redeem, cfg.PublicURL, grant, nil, ""), 200).Cookies()
	if !h.Auth.RequireFresh(httptest.NewRecorder(), request("PUT", "/api/v1/supervisor/binding", cfg.PublicURL, "", cookies, cookies[1].Value)) {
		t.Fatal("a recent native auth must satisfy the protected local operation")
	}
	grant, _ = h.Auth.DesktopGrant(true)
	if got := call(request("POST", redeem, cfg.PublicURL, grant, cookies, ""), 200).Cookies(); len(got) != 0 {
		t.Fatalf("native reauth replaced session: %v", got)
	}
	h.Auth.LockDesktop()
	call(request("GET", "/api/v1/capabilities", "", "", cookies, ""), 401)
	call(request("GET", "/sw.js", "", "", nil, ""), 404)
}

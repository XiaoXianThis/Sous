package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/supervisor"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

type env struct {
	srv    *httptest.Server
	client *http.Client
	log    *eventlog.Log
	auth   *auth.Auth
	main   string
}

func newEnv(t *testing.T, dev bool) *env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	web := t.TempDir()
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>sous</title>"), 0o644)
	os.MkdirAll(filepath.Join(web, "assets"), 0o755)
	os.WriteFile(filepath.Join(web, "assets", "app.js"), []byte("console.log(1)"), 0o644)

	cfg := &config.Hub{PublicURL: "http://localhost:8787", Dev: dev, WebDir: web}
	log := eventlog.New(st, event.NewBus())
	a, err := auth.New(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	mock, _ := provider.New(provider.Config{Name: "mock", Kind: "mock"})
	ctx, cancel := context.WithCancel(context.Background())
	sup := supervisor.New(supervisor.Options{Log: log, Provider: mock, Model: "m", Tools: tool.NewRegistry(tool.CurrentTime{})})
	sup.Start(ctx)
	main, _ := log.EnsureMainThread(ctx)

	srv := httptest.NewServer(httpapi.New(cfg, log, a))
	jar, _ := cookiejar.New(nil)
	t.Cleanup(func() { srv.Close(); cancel(); sup.Wait(); st.Close() })
	return &env{srv: srv, client: &http.Client{Jar: jar}, log: log, auth: a, main: main}
}

func (e *env) csrf() string {
	u, _ := url.Parse(e.srv.URL)
	for _, c := range e.client.Jar.Cookies(u) {
		if c.Name == auth.CSRFCookie {
			return c.Value
		}
	}
	return ""
}

func (e *env) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if csrf := e.csrf(); csrf != "" {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (e *env) login(t *testing.T) {
	t.Helper()
	if code := e.do(t, "POST", "/api/v1/auth/dev-login", nil, nil); code != 200 {
		t.Fatalf("dev login: %d", code)
	}
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	e := newEnv(t, true)
	if code := e.do(t, "GET", "/api/v1/threads", nil, nil); code != 401 {
		t.Fatalf("threads without login: %d", code)
	}
	var me map[string]bool
	e.do(t, "GET", "/api/v1/me", nil, &me)
	if me["logged_in"] || me["has_passkey"] || !me["dev_login"] {
		t.Fatalf("me: %v", me)
	}
}

func TestDevLoginDisabledOutsideDev(t *testing.T) {
	e := newEnv(t, false)
	if code := e.do(t, "POST", "/api/v1/auth/dev-login", nil, nil); code != 404 {
		t.Fatalf("dev login allowed: %d", code)
	}
}

func TestDevLoginRejectsProxiedRequests(t *testing.T) {
	e := newEnv(t, true)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/dev-login", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("proxied dev login: %d", resp.StatusCode)
	}
}

func TestCSRFRequiredForWrites(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/threads", strings.NewReader(`{"title":"x"}`))
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("write without CSRF: %d", resp.StatusCode)
	}
}

func TestChatRoundTripOverRESTAndWebSocket(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)

	var threads struct{ Threads []store.Thread }
	e.do(t, "GET", "/api/v1/threads", nil, &threads)
	if len(threads.Threads) != 1 || threads.Threads[0].Kind != "main" || threads.Threads[0].ID != e.main {
		t.Fatalf("threads: %+v", threads)
	}

	// Connect the event stream with the session cookie.
	u, _ := url.Parse(e.srv.URL)
	hdr := http.Header{}
	for _, c := range e.client.Jar.Cookies(u) {
		hdr.Add("Cookie", c.Name+"="+c.Value)
	}
	hdr.Set("Origin", "http://localhost:8787")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws?since=0", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	var posted struct {
		MessageID string `json:"message_id"`
	}
	if code := e.do(t, "POST", "/api/v1/threads/"+e.main+"/messages", map[string]string{"content": "你好"}, &posted); code != 202 {
		t.Fatalf("post: %d", code)
	}

	var sawBacklogThread, sawUser, sawDelta bool
	var reply event.MessagePayload
	for reply.Content == "" {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("ws read: %v (thread=%v user=%v delta=%v)", err, sawBacklogThread, sawUser, sawDelta)
		}
		var ev event.Event
		json.Unmarshal(raw, &ev)
		switch ev.Type {
		case event.ThreadCreated:
			sawBacklogThread = true
		case event.MessageUser:
			sawUser = true
		case event.MessageAssistantDelta:
			sawDelta = ev.Seq == 0
		case event.MessageAssistant:
			ev.Decode(&reply)
		}
	}
	if !sawBacklogThread || !sawUser || !sawDelta || !strings.Contains(reply.Content, "你好") {
		t.Fatalf("thread=%v user=%v delta=%v reply=%+v", sawBacklogThread, sawUser, sawDelta, reply)
	}

	var page struct {
		Messages []store.Message
		LastSeq  int64 `json:"last_seq"`
	}
	e.do(t, "GET", "/api/v1/threads/"+e.main+"/messages", nil, &page)
	if len(page.Messages) != 2 || page.Messages[0].ID != posted.MessageID || page.LastSeq == 0 {
		t.Fatalf("history: %+v", page)
	}
}

func TestCreateThreadAndValidation(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	var th store.Thread
	if code := e.do(t, "POST", "/api/v1/threads", map[string]string{"title": "  项目 X  "}, &th); code != 201 || th.Title != "项目 X" || th.Kind != "chat" {
		t.Fatalf("create: %d %+v", code, th)
	}
	if code := e.do(t, "POST", "/api/v1/threads/"+th.ID+"/messages", map[string]string{"content": "   "}, nil); code != 400 {
		t.Fatalf("empty message: %d", code)
	}
	if code := e.do(t, "POST", "/api/v1/threads/nope/messages", map[string]string{"content": "hi"}, nil); code != 404 {
		t.Fatalf("unknown thread: %d", code)
	}
	if code := e.do(t, "GET", "/api/v1/nope", nil, nil); code != 404 {
		t.Fatalf("unknown api: %d", code)
	}
}

func TestLogout(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	e.do(t, "POST", "/api/v1/auth/logout", nil, nil)
	if code := e.do(t, "GET", "/api/v1/threads", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

func TestSetupTokenGatesRegistration(t *testing.T) {
	e := newEnv(t, true)
	if code := e.do(t, "POST", "/api/v1/auth/passkey/register/begin", map[string]string{"setup_token": "wrong"}, nil); code != 403 {
		t.Fatalf("bad token: %d", code)
	}
	tok, err := e.auth.NewSetupToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var opts map[string]any
	if code := e.do(t, "POST", "/api/v1/auth/passkey/register/begin", map[string]string{"setup_token": tok}, &opts); code != 200 {
		t.Fatalf("good token: %d", code)
	}
	pk, _ := opts["publicKey"].(map[string]any)
	if rp, _ := pk["rp"].(map[string]any); rp["id"] != "localhost" {
		t.Fatalf("options: %v", opts)
	}
	if code := e.do(t, "POST", "/api/v1/auth/passkey/login/begin", nil, nil); code != 409 {
		t.Fatalf("login without passkey: %d", code)
	}
}

func TestStaticFallback(t *testing.T) {
	e := newEnv(t, true)
	for _, p := range []string{"/", "/threads/abc", "/setup?token=x"} {
		resp, err := http.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "<title>sous</title>") {
			t.Fatalf("%s: %q", p, body)
		}
	}
	resp, _ := http.Get(e.srv.URL + "/assets/app.js")
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset cache header: %q", resp.Header.Get("Cache-Control"))
	}
	resp, _ = http.Get(e.srv.URL + "/assets/index-old.js")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("missing asset: %d, want 404", resp.StatusCode)
	}
	resp, _ = http.Get(e.srv.URL + "/../../etc/passwd")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "root:") {
		t.Fatal("path traversal")
	}
}

func TestMemoriesCRUD(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	var m store.Memory
	if code := e.do(t, "POST", "/api/v1/memories", map[string]string{"kind": "profile", "content": " 喜欢用 Go 写后端 "}, &m); code != 201 || m.Content != "喜欢用 Go 写后端" {
		t.Fatalf("create: %d %+v", code, m)
	}
	if code := e.do(t, "POST", "/api/v1/memories", map[string]string{"kind": "bogus", "content": "x"}, nil); code != 400 {
		t.Fatalf("bad kind: %d", code)
	}
	var list struct {
		Memories []store.Memory
		Kinds    []string
	}
	e.do(t, "GET", "/api/v1/memories?q="+url.QueryEscape("后端用什么写"), nil, &list)
	if len(list.Memories) != 1 || list.Memories[0].ID != m.ID || len(list.Kinds) != 4 {
		t.Fatalf("search: %+v", list)
	}
	var upd store.Memory
	if code := e.do(t, "PUT", "/api/v1/memories/"+m.ID, map[string]string{"kind": "profile", "content": "喜欢用 Rust 写后端"}, &upd); code != 200 || upd.Content != "喜欢用 Rust 写后端" {
		t.Fatalf("update: %d %+v", code, upd)
	}
	if code := e.do(t, "PUT", "/api/v1/memories/nope", map[string]string{"kind": "note", "content": "x"}, nil); code != 404 {
		t.Fatalf("update missing: %d", code)
	}
	if code := e.do(t, "DELETE", "/api/v1/memories/"+m.ID, nil, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	e.do(t, "GET", "/api/v1/memories", nil, &list)
	if len(list.Memories) != 0 {
		t.Fatalf("after delete: %+v", list.Memories)
	}
	if code := e.do(t, "DELETE", "/api/v1/memories/"+m.ID, nil, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, false)
	resp, err := e.client.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self';", "frame-ancestors 'none'", "connect-src 'self' ws://localhost:8787"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers %v", resp.Header)
	}
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS over plain http")
	}
}

func TestRequestBodyIsCapped(t *testing.T) {
	e := newEnv(t, true)
	e.login(t)
	huge := map[string]string{"title": strings.Repeat("x", 5<<20)}
	if code := e.do(t, "POST", "/api/v1/threads", huge, nil); code != 400 {
		t.Fatalf("5 MB body: %d", code)
	}
}

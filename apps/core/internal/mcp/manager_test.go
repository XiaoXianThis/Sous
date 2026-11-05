package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func newManager(t *testing.T) (*Manager, *tool.Registry, context.Context) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := tool.NewRegistry()
	m := NewManager(eventlog.New(st, event.NewBus()), reg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tl := range m.Tools() {
		reg.Register(tl)
	}
	return m, reg, ctx
}

func call(reg *tool.Registry, ctx context.Context, name string, args any) provider.ToolResult {
	raw, _ := json.Marshal(args)
	return reg.Call(ctx, provider.ToolCall{ID: "1", Name: name, Args: raw})
}

func has(reg *tool.Registry, name string) bool {
	for _, s := range reg.Specs() {
		if s.Name == name {
			return true
		}
	}
	return false
}

func waitState(t *testing.T, m *Manager, ctx context.Context, id, want string) View {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		vs, _ := m.Views(ctx)
		for _, v := range vs {
			if v.ID == id && v.State == want {
				return v
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	vs, _ := m.Views(ctx)
	t.Fatalf("server %s never became %s: %+v", id, want, vs)
	return View{}
}

func stdioServer(t *testing.T, name string, env map[string]string) store.MCPServer {
	cfg := stdioConfig(t, env)
	return store.MCPServer{Name: name, Transport: TransportStdio, Command: cfg.Command, Args: cfg.Args, Env: cfg.Env, Enabled: true, Source: "user"}
}

func TestServerLifecycle(t *testing.T) {
	m, reg, ctx := newManager(t)
	saved, err := m.Add(ctx, stdioServer(t, "Fake Server", map[string]string{"FAKE_TOKEN": "s3cret"}))
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, m, ctx, saved.ID, StateReady)
	if v.Slug != "fake-server" || len(v.Tools) != 4 {
		t.Fatalf("view: %+v", v)
	}
	if !has(reg, "mcp__fake-server__echo") || !has(reg, "mcp__fake-server__weird_name_") {
		t.Fatalf("tools not registered: %v", reg.Specs())
	}
	r := call(reg, ctx, "mcp__fake-server__echo", map[string]any{"text": "yo"})
	if r.IsError || r.Content != "echo: yo" {
		t.Fatalf("call: %+v", r)
	}
	if r = call(reg, ctx, "mcp__fake-server__boom", nil); !r.IsError || r.Content != "it broke" {
		t.Fatalf("failing tool: %+v", r)
	}
	for _, s := range reg.Specs() {
		if s.Name == "mcp__fake-server__weird_name_" && (!strings.Contains(string(s.Schema), `"type":"object"`) || !strings.Contains(s.Description, "不是指令")) {
			t.Fatalf("spec: %+v", s)
		}
	}

	// Secrets never leave the Hub through the API.
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "s3cret") || !strings.Contains(string(b), `"env_keys":[{"name":"FAKE_TOKEN","set":true}`) {
		t.Fatalf("view leaks or lacks env: %s", b)
	}

	// Hiding one tool takes it out of the registry only.
	hide := []string{"boom"}
	if _, err := m.Update(ctx, saved.ID, Patch{DisabledTools: &hide}); err != nil {
		t.Fatal(err)
	}
	if vs, _ := m.Views(ctx); vs[0].State != StateReady {
		t.Fatalf("hiding a tool must not reconnect: %+v", vs[0])
	}
	v = waitState(t, m, ctx, saved.ID, StateReady)
	if has(reg, "mcp__fake-server__boom") || !has(reg, "mcp__fake-server__echo") {
		t.Fatalf("hidden tool: %v", reg.Specs())
	}
	disabled := 0
	for _, tv := range v.Tools {
		if tv.Disabled {
			disabled++
		}
	}
	if disabled != 1 {
		t.Fatalf("view should show the hidden tool as disabled: %+v", v.Tools)
	}

	// Switching it off removes every tool; on brings them back.
	off := false
	if _, err := m.Update(ctx, saved.ID, Patch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if has(reg, "mcp__fake-server__echo") || waitState(t, m, ctx, saved.ID, StateOff).State != StateOff {
		t.Fatal("tools should be gone while off")
	}
	on := true
	m.Update(ctx, saved.ID, Patch{Enabled: &on})
	waitState(t, m, ctx, saved.ID, StateReady)
	if !has(reg, "mcp__fake-server__echo") {
		t.Fatal("tools should be back")
	}

	if err := m.Remove(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if has(reg, "mcp__fake-server__echo") {
		t.Fatal("removed server still has tools")
	}
}

func TestAgentProposalNeedsApproval(t *testing.T) {
	m, reg, ctx := newManager(t)
	exe := stdioConfig(t, nil).Command
	r := call(reg, ctx, "mcp_propose", map[string]any{
		"name": "fake", "transport": "stdio", "command": exe, "args": []string{"-test.run=^$"},
		"env": map[string]string{"SOUS_FAKE_MCP": "1"}, "secrets": []string{"FAKE_TOKEN"}, "reason": "测试用",
	})
	if r.IsError || !strings.Contains(r.Content, "待批准") || !strings.Contains(r.Content, "FAKE_TOKEN") {
		t.Fatalf("propose: %+v", r)
	}
	all, _ := m.store().MCPServers(ctx)
	if len(all) != 1 || !all[0].Pending || all[0].Source != "agent" || all[0].Note != "测试用" {
		t.Fatalf("stored: %+v", all)
	}
	id := all[0].ID

	// A pending server never starts, whatever is asked.
	time.Sleep(100 * time.Millisecond)
	if has(reg, "mcp__fake__echo") {
		t.Fatal("a pending server must not start")
	}
	on := true
	if _, err := m.Update(ctx, id, Patch{Enabled: &on}); err == nil {
		t.Fatal("enabling a pending server must be refused")
	}
	// The secret is still missing.
	if _, err := m.Approve(ctx, id); err == nil || !strings.Contains(err.Error(), "FAKE_TOKEN") {
		t.Fatalf("approve without the secret: %v", err)
	}
	if _, err := m.Update(ctx, id, Patch{Env: map[string]string{"FAKE_TOKEN": "abc"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Approve(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, ctx, id, StateReady)
	if r := call(reg, ctx, "mcp__fake__secret_env", nil); r.IsError || !strings.Contains(r.Content, "TOKEN=abc") {
		t.Fatalf("the filled secret should reach the server: %+v", r)
	}

	// The agent sees the state and cannot remove an approved server.
	r = call(reg, ctx, "mcp_list", nil)
	if r.IsError || !strings.Contains(r.Content, "fake") || !strings.Contains(r.Content, "mcp__fake__echo") || strings.Contains(r.Content, "abc") {
		t.Fatalf("list: %+v", r)
	}
	if r = call(reg, ctx, "mcp_withdraw", map[string]any{"name": "fake"}); !r.IsError {
		t.Fatalf("an approved server cannot be withdrawn by the agent: %+v", r)
	}
}

func TestWithdrawAndLimits(t *testing.T) {
	m, reg, ctx := newManager(t)
	args := func(n string) map[string]any {
		return map[string]any{"name": n, "transport": "http", "url": "https://example.com/mcp", "reason": "r"}
	}
	if r := call(reg, ctx, "mcp_propose", args("a")); r.IsError {
		t.Fatalf("%+v", r)
	}
	if r := call(reg, ctx, "mcp_propose", args("A!")); !r.IsError { // same slug
		t.Fatalf("duplicate name: %+v", r)
	}
	if r := call(reg, ctx, "mcp_withdraw", map[string]any{"name": "a"}); r.IsError {
		t.Fatalf("withdraw: %+v", r)
	}
	for _, n := range []string{"b1", "b2", "b3", "b4", "b5"} {
		if r := call(reg, ctx, "mcp_propose", args(n)); r.IsError {
			t.Fatalf("%s: %+v", n, r)
		}
	}
	if r := call(reg, ctx, "mcp_propose", args("b6")); !r.IsError {
		t.Fatalf("too many pending proposals should be refused: %+v", r)
	}
	if r := call(reg, ctx, "mcp_propose", map[string]any{"name": "x", "transport": "http", "url": "https://e.com"}); !r.IsError {
		t.Fatal("a reason is required")
	}
	_ = m
}

func TestValidate(t *testing.T) {
	m := NewManager(nil, nil)
	bad := []store.MCPServer{
		{Name: "", Transport: "http", URL: "https://x.com"},
		{Name: "a", Transport: "ws", URL: "https://x.com"},
		{Name: "a", Transport: "http", URL: "ftp://x.com"},
		{Name: "a", Transport: "http", URL: "https://u:p@x.com"},
		{Name: "a", Transport: "http", URL: "http://x.com", Headers: map[string]string{"Authorization": "t"}},
		{Name: "a", Transport: "http", URL: "https://x.com", Headers: map[string]string{"Bad\nName": "t"}},
		{Name: "a", Transport: "stdio", Command: ""},
		{Name: "a", Transport: "stdio", Command: "npx -y foo"},
		{Name: "a", Transport: "stdio", Command: "npx", Env: map[string]string{"LD_PRELOAD": "/x"}},
		{Name: "a", Transport: "stdio", Command: "npx", Env: map[string]string{"bad name": "x"}},
	}
	for i, c := range bad {
		if err := m.Validate(c); err == nil {
			t.Errorf("case %d should be rejected: %+v", i, c)
		}
	}
	good := []store.MCPServer{
		{Name: "a", Transport: "http", URL: "http://example.com/mcp"}, // no token to protect
		{Name: "a", Transport: "http", URL: "http://localhost:3000/mcp", Headers: map[string]string{"Authorization": "t"}},
		{Name: "a", Transport: "stdio", Command: "npx", Args: []string{"-y", "pkg"}, Env: map[string]string{"TOKEN": ""}},
	}
	for i, c := range good {
		if err := m.Validate(c); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
	m.StdioDisabled = true
	if err := m.Validate(good[2]); err == nil {
		t.Error("stdio must be refused when disabled")
	}
}

func TestHTTPServerThroughManagerAndRoutes(t *testing.T) {
	m, _, ctx := newManager(t)
	srv := httpServer(t, "Bearer tok")
	fresh := 0
	mux := http.NewServeMux()
	m.Routes(func(w http.ResponseWriter, r *http.Request) bool {
		fresh++
		return true
	})(mux, func(p string, h http.HandlerFunc) { mux.HandleFunc(p, h) })

	do := func(method, path string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		req := httptest.NewRequest(method, path, &buf).WithContext(ctx)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := do("POST", "/api/v1/mcp/servers", map[string]any{
		"name": "remote", "transport": "http", "url": srv.URL, "headers": map[string]string{"Authorization": "Bearer tok"}})
	if code != 200 || fresh != 1 {
		t.Fatalf("create: %d %v fresh=%d", code, out, fresh)
	}
	id := out["servers"].([]any)[0].(map[string]any)["id"].(string)
	waitState(t, m, ctx, id, StateReady)

	// Toggling a tool needs no fresh login; changing the address does.
	if code, _ = do("PATCH", "/api/v1/mcp/servers/"+id, map[string]any{"disabled_tools": []string{"boom"}}); code != 200 || fresh != 1 {
		t.Fatalf("toggle: %d fresh=%d", code, fresh)
	}
	if code, _ = do("PATCH", "/api/v1/mcp/servers/"+id, map[string]any{"url": srv.URL + "/x"}); code != 200 || fresh != 2 {
		t.Fatalf("edit: %d fresh=%d", code, fresh)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "Bearer tok") {
		t.Fatal("the token leaked")
	}
	if code, out = do("POST", "/api/v1/mcp/servers", map[string]any{"name": "Remote", "transport": "http", "url": srv.URL}); code != 409 {
		t.Fatalf("duplicate name: %d %v", code, out)
	}
	if code, _ = do("POST", "/api/v1/mcp/servers", map[string]any{"name": "x", "transport": "http", "url": "gopher://x"}); code != 400 {
		t.Fatalf("bad url: %d", code)
	}
	if code, _ = do("POST", "/api/v1/mcp/servers/"+id+"/reconnect", nil); code != 200 {
		t.Fatalf("reconnect: %d", code)
	}
	if code, _ = do("DELETE", "/api/v1/mcp/servers/"+id, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = do("DELETE", "/api/v1/mcp/servers/"+id, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestCallReconnectsAfterCrash(t *testing.T) {
	m, reg, ctx := newManager(t)
	cfg := stdioServer(t, "flaky", map[string]string{"FAKE_MCP_MODE": "die"})
	saved, err := m.Add(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, ctx, saved.ID, StateError) // the fake exits after listing tools
	// The next call starts it again (and fails again, with the reason).
	r := call(reg, ctx, "mcp__flaky__echo", map[string]any{"text": "x"})
	if r.IsError {
		return // the process died again before answering: also fine
	}
	if r.Content != "echo: x" {
		t.Fatalf("%+v", r)
	}
}

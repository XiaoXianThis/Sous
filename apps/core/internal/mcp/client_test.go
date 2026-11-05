package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStdioClient(t *testing.T) {
	t.Setenv("HUB_SECRET", "must-not-leak")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Connect(ctx, stdioConfig(t, map[string]string{"FAKE_TOKEN": "abc"}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ServerName != "fake" || c.Protocol != "2025-06-18" {
		t.Fatalf("handshake: %+v", c)
	}
	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 4 || tools[0].Name != "echo" || !tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("tools: %v %v", tools, err)
	}
	r, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || formatResult(r) != "echo: hi" {
		t.Fatalf("echo: %v %v", r, err)
	}
	// The Hub's own environment is not passed on; the configured variables are.
	r, _ = c.CallTool(ctx, "secret_env", nil)
	if got := formatResult(r); got != "TOKEN=abc HOME_SET=" {
		t.Fatalf("env: %q", got)
	}
	r, err = c.CallTool(ctx, "boom", nil)
	if err != nil || !r.IsError {
		t.Fatalf("boom: %v %v", r, err)
	}
	if _, err := c.CallTool(ctx, "nope", nil); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown: %v", err)
	}
}

func TestStdioServerDies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Connect(ctx, stdioConfig(t, map[string]string{"FAKE_MCP_MODE": "die"}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the connection did not notice the process exit")
	}
	if _, err := c.CallTool(ctx, "echo", nil); err == nil {
		t.Fatal("a call on a dead connection must fail")
	}
	if !strings.Contains(c.Diagnostics(), "out of cheese") {
		t.Fatalf("stderr not kept: %q", c.Diagnostics())
	}
}

func TestStdioBadCommand(t *testing.T) {
	_, err := Connect(context.Background(), Config{Transport: TransportStdio, Command: "/definitely/not/here"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestHTTPClient(t *testing.T) {
	srv := httpServer(t, "Bearer tok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := Connect(ctx, Config{Transport: TransportHTTP, URL: srv.URL}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("without the token: %v", err)
	}
	c, err := Connect(ctx, Config{Transport: TransportHTTP, URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer tok"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 4 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	// The answer arrives as an SSE stream behind a notification.
	r, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"over http"}`))
	if err != nil || formatResult(r) != "echo: over http" {
		t.Fatalf("echo: %v %v", r, err)
	}
}

func TestFormatResult(t *testing.T) {
	r := CallResult{Content: []Content{
		{Type: "text", Text: "a"},
		{Type: "image", MIMEType: "image/png", Data: strings.Repeat("A", 400)},
		{Type: "resource_link", URI: "file:///x"},
	}}
	got := formatResult(r)
	if !strings.Contains(got, "a\n[image：image/png，300 字节") || !strings.Contains(got, "file:///x") {
		t.Fatalf("format: %q", got)
	}
	if got := formatResult(CallResult{StructuredContent: json.RawMessage(`{"n":1}`)}); got != `{"n":1}` {
		t.Fatalf("structured: %q", got)
	}
	long := formatResult(CallResult{Content: []Content{{Type: "text", Text: strings.Repeat("字", maxResultChars+10)}}})
	if !strings.Contains(long, "已截断") {
		t.Fatal("not truncated")
	}
}

func TestExposedName(t *testing.T) {
	taken := map[string]string{}
	a := exposedName("mcp__gh__", "weird name!", taken)
	if a != "mcp__gh__weird_name_" {
		t.Fatalf("%q", a)
	}
	taken[a] = "x"
	if b := exposedName("mcp__gh__", "weird name!", taken); b == a || len(b) > 64 {
		t.Fatalf("clash: %q", b)
	}
	long := exposedName("mcp__gh__", strings.Repeat("x", 100), taken)
	if len(long) != 64 {
		t.Fatalf("long: %d %q", len(long), long)
	}
	if Slug("GitHub (work)!") != "github-work" || Slug("！！") != "server" {
		t.Fatalf("slug: %q", Slug("GitHub (work)!"))
	}
}

package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The test binary doubles as a stdio MCP server: with SOUS_FAKE_MCP set,
// TestMain serves the protocol on stdin/stdout instead of running tests.
//
// FAKE_MCP_MODE: "" (normal), "die" (exits after tools/list), "slow" never answers tools/call.
func TestMain(m *testing.M) {
	if os.Getenv("SOUS_FAKE_MCP") == "1" {
		serveFake(os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

var fakeTools = `[
 {"name":"echo","description":"Echo the text back.","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]},"annotations":{"readOnlyHint":true}},
 {"name":"boom","description":"Always fails.","inputSchema":{"type":"object"}},
 {"name":"weird name!","description":"","inputSchema":{"properties":{}}},
 {"name":"secret_env","description":"Reports an environment variable.","inputSchema":{"type":"object"}}
]`

// handle answers one JSON-RPC message; ok is false for notifications.
func handle(line []byte, env func(string) string) (resp map[string]any, ok bool) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &m) != nil || len(m.ID) == 0 {
		return nil, false
	}
	out := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID)}
	switch m.Method {
	case "initialize":
		out["result"] = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "fake", "version": "1.0"}}
	case "tools/list":
		out["result"] = map[string]any{"tools": json.RawMessage(fakeTools)}
	case "tools/call":
		var p struct {
			Name      string
			Arguments map[string]any
		}
		json.Unmarshal(m.Params, &p)
		switch p.Name {
		case "echo":
			out["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": "echo: " + fmt.Sprint(p.Arguments["text"])}}}
		case "boom":
			out["result"] = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "it broke"}}}
		case "secret_env":
			out["result"] = map[string]any{"content": []map[string]any{{"type": "text", "text": "TOKEN=" + env("FAKE_TOKEN") + " HOME_SET=" + env("HUB_SECRET")}}}
		default:
			out["error"] = map[string]any{"code": -32602, "message": "unknown tool"}
		}
	default:
		out["error"] = map[string]any{"code": -32601, "message": "no such method"}
	}
	return out, true
}

func serveFake(in *os.File, out *os.File) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		resp, ok := handle(sc.Bytes(), os.Getenv)
		if !ok {
			continue
		}
		b, _ := json.Marshal(resp)
		w.Write(append(b, '\n'))
		w.Flush()
		if os.Getenv("FAKE_MCP_MODE") == "die" && strings.Contains(sc.Text(), "tools/list") {
			fmt.Fprintln(os.Stderr, "fatal: out of cheese")
			os.Exit(3)
		}
	}
}

// stdioConfig runs this test binary as the server.
func stdioConfig(t *testing.T, env map[string]string) Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e := map[string]string{"SOUS_FAKE_MCP": "1"}
	for k, v := range env {
		e[k] = v
	}
	return Config{Transport: TransportStdio, Command: exe, Args: []string{"-test.run=^$"}, Env: e}
}

// httpServer is a streamable HTTP server: initialize hands out a session,
// tools/call answers as an SSE stream, the rest as plain JSON.
func httpServer(t *testing.T, wantAuth string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var raw json.RawMessage
		json.NewDecoder(r.Body).Decode(&raw)
		resp, ok := handle(raw, func(string) string { return "" })
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var m struct{ Method string }
		json.Unmarshal(raw, &m)
		if m.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "sess-1")
		} else if r.Header.Get("Mcp-Session-Id") != "sess-1" || r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
			http.Error(w, "missing session headers", http.StatusBadRequest)
			return
		}
		b, _ := json.Marshal(resp)
		if m.Method == "tools/call" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\nevent: message\ndata: %s\n\n", b)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

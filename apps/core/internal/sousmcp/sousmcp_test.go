package sousmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServe(t *testing.T) {
	var calls []string
	call := func(_ context.Context, method string, c Call) (Answer, error) {
		calls = append(calls, method+":"+c.Text+c.Done)
		return Answer{Text: "ok " + method}, nil
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ask_supervisor","arguments":{"question":"用哪个端口？"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"report_progress","arguments":{"text":""}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"report_result","arguments":{"done":"改好了","verified":"跑了测试"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"nope"}`,
	}, "\n")
	var out bytes.Buffer
	Serve(context.Background(), strings.NewReader(in), &out, call)
	var replies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		json.Unmarshal([]byte(line), &m)
		replies = append(replies, m)
	}
	if len(replies) != 7 {
		t.Fatalf("replies: %s", out.String())
	}
	if pv := replies[0]["result"].(map[string]any)["protocolVersion"]; pv != "2025-03-26" {
		t.Fatalf("protocol version %v", pv)
	}
	if n := len(replies[1]["result"].(map[string]any)["tools"].([]any)); n != 3 {
		t.Fatalf("tools: %d", n)
	}
	text := func(i int) (string, bool) {
		r := replies[i]["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
	}
	if s, isErr := text(2); s != "ok run.ask" || isErr {
		t.Fatalf("ask: %q %v", s, isErr)
	}
	if _, isErr := text(3); !isErr {
		t.Fatal("empty progress accepted")
	}
	if _, isErr := text(4); !isErr {
		t.Fatal("unknown tool accepted")
	}
	if replies[6]["error"] == nil {
		t.Fatal("unknown method accepted")
	}
	if strings.Join(calls, ",") != "run.ask:用哪个端口？,run.result:改好了" {
		t.Fatalf("calls: %v", calls)
	}
}

func TestReportStatusIsOptionalAndValidated(t *testing.T) {
	for _, tc := range []struct {
		name, args, want string
		valid            bool
	}{
		{"report_progress", `{"text":"工作完成","status":"completed"}`, "completed", true},
		{"report_result", `{"done":"服务已启动","verified":"通过"}`, "", true},
		{"report_result", `{"done":"部署失败","verified":"失败","status":"failed"}`, "failed", true},
		{"report_result", `{"done":"完成","status":"completed"}`, "completed", true},
		{"report_progress", `{"text":"工作完成","status":"failed"}`, "", false},
		{"report_result", `{"done":"完成","status":"done"}`, "", false},
	} {
		called := false
		_, err := callTool(context.Background(), tc.name, json.RawMessage(tc.args), func(_ context.Context, _ string, c Call) (Answer, error) {
			called = true
			if c.Status != tc.want {
				t.Fatalf("status: %q want %q", c.Status, tc.want)
			}
			return Answer{}, nil
		})
		if (err == nil) != tc.valid || called != tc.valid {
			t.Fatalf("%s: called=%v err=%v", tc.args, called, err)
		}
	}
	for _, tool := range tools {
		if tool["name"] == "ask_supervisor" {
			continue
		}
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if props["status"] == nil {
			t.Fatalf("missing status schema: %v", tool["name"])
		}
	}
}

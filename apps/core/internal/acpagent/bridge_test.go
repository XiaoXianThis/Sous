package acpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func TestTurnToolBoundary(t *testing.T) {
	ctx := context.Background()
	h, err := hub.Open(config.ForLocalWorkspace(t.TempDir()), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	main, err := h.Log.EnsureMainThread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Tasks.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ours", "other"} {
		_, err := h.Log.Append(ctx, event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{ProjectID: name, Name: name, RepoURL: "https://private.example/host-must-not-leak"}))
		if err != nil {
			t.Fatal(err)
		}
	}
	deps := hub.AgentDeps{Log: h.Log, Tasks: h.Tasks, Tools: tool.NewRegistry(tool.CurrentTime{}, tool.MemorySearch{Log: h.Log})}
	stages := []struct {
		name     string
		turn     tool.Turn
		internal bool
	}{
		{name: "project", turn: tool.Turn{ThreadID: main, ProjectID: "ours", FirstSeq: 1}},
		{name: "automatic", turn: tool.Turn{ThreadID: main, FirstSeq: 2}, internal: true},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			turn, cancel := context.WithTimeout(tool.WithTurn(ctx, stage.turn), 5*time.Second)
			defer cancel()
			b, err := openBridge(turn, registry(deps), stage.internal)
			if err != nil {
				t.Fatal(err)
			}
			defer b.close()
			call := func(token, origin, method, args string, code int, match string) string {
				t.Helper()
				req := httptest.NewRequest("POST", "http://"+b.address+"/mcp", strings.NewReader(fmt.Sprintf(`{"method":%q,"params":%s}`, method, args)))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Origin", origin)
				w := httptest.NewRecorder()
				b.serve(w, req)
				if w.Code != code || !strings.Contains(w.Body.String(), match) {
					t.Fatalf("%s got %d %s", method, w.Code, w.Body)
				}
				return w.Body.String()
			}
			call("wrong", "", "tools/list", "{}", http.StatusNotFound, "")
			call(b.token, "https://example.test", "tools/list", "{}", http.StatusNotFound, "")
			tools := call(b.token, "", "tools/list", "{}", http.StatusOK, `"project_list"`)
			if strings.Contains(tools, "git_push") || strings.Contains(tools, "task_create") == stage.internal {
				t.Fatal(tools)
			}
			if stage.internal {
				call(b.token, "", "tools/call", `{"name":"task_create","arguments":{"title":"forbidden"}}`, 200, "不能创建任务")
			} else {
				list := call(b.token, "", "tools/call", `{"name":"project_list"}`, 200, "ours")
				if strings.Contains(list, "other") || strings.Contains(list, "host-must-not-leak") {
					t.Fatal(list)
				}
				call(b.token, "", "tools/call", `{"name":"memory_search","arguments":{"query":"anything"}}`, 200, "不能读取全局记忆")
				call(b.token, "", "tools/call", `{"name":"task_create","arguments":{"title":"forbidden","project_id":"other"}}`, 200, "只能在当前项目")
				const allowed = `{"name":"task_create","arguments":{"title":"草稿"}}`
				first := call(b.token, "", "tools/call", allowed, 200, "未派发")
				second := call(b.token, "", "tools/call", allowed, 200, "未派发")
				if first != second || !json.Valid([]byte(first)) {
					t.Fatalf("idempotence failed: %s / %s", first, second)
				}
				if task, err := h.Tasks.FileSupervisorDraft(turn, "", "草稿", ""); err != nil || task.Status != "inbox" || task.ProjectID != "ours" {
					t.Fatalf("bad draft: %+v %v", task, err)
				}
				call(b.token, "", "tools/call", `{"name":"task_create","arguments":{"title":"also refused"}}`, 200, "不会再重复创建")
			}
			cancel()
			call(b.token, "", "tools/call", `{"name":"task_create","arguments":{"title":"late"}}`, http.StatusGone, "turn ended")
		})
	}
}

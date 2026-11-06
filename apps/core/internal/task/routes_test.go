package task_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/notify"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

type client struct {
	t    *testing.T
	base string
	c    *http.Client
	csrf string
	log  *eventlog.Log
}

func setup(t *testing.T) *client {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := eventlog.New(st, event.NewBus())
	cfg := &config.Hub{PublicURL: "http://localhost:8787", Dev: true}
	a, _ := auth.New(st, cfg)
	nodes, _ := nodehub.New(log, t.TempDir())
	eng := task.New(log, nodes)
	vapid, _ := notify.LoadVAPID(t.TempDir())
	n := &notify.Notifier{Log: log, Sender: &notify.Sender{VAPID: vapid}}
	srv := httptest.NewServer(httpapi.New(cfg, log, a, nodes.Routes, eng.Routes, n.Routes(func(context.Context) map[string]string { return map[string]string{"supervisor": "deepseek/x"} })))
	log.EnsureMainThread(context.Background())
	t.Cleanup(func() { srv.Close(); st.Close() })

	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: srv.URL, c: &http.Client{Jar: jar}, log: log}
	c.c.Post(srv.URL+"/api/v1/auth/dev-login", "", nil)
	u, _ := url.Parse(srv.URL)
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "sous_csrf" {
			c.csrf = ck.Value
		}
	}
	// A machine to register projects on.
	log.Append(context.Background(), event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{NodeID: "n1", Name: "Mac"}))
	return c
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf)
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestProjectAndTaskEndpoints(t *testing.T) {
	c := setup(t)
	var p store.Project
	if code := c.do("POST", "/api/v1/projects", map[string]any{"name": "demo", "nodes": []map[string]string{{"node_id": "n1", "path": "/src/demo"}}}, &p); code != 201 || len(p.Nodes) != 1 {
		t.Fatalf("create project: %d %+v", code, p)
	}
	if code := c.do("POST", "/api/v1/projects", map[string]any{"name": "DEMO"}, nil); code != 409 {
		t.Fatalf("duplicate name: %d", code)
	}
	if code := c.do("POST", "/api/v1/projects", map[string]any{"name": "x", "nodes": []map[string]string{{"node_id": "nope", "path": "/a"}}}, nil); code != 400 {
		t.Fatalf("unknown machine: %d", code)
	}

	var tk store.Task
	if code := c.do("POST", "/api/v1/tasks", map[string]any{"title": "加个按钮", "project_id": p.ID}, &tk); code != 201 || tk.Status != "inbox" || tk.Kind != "code" {
		t.Fatalf("create task: %d %+v", code, tk)
	}
	if code := c.do("POST", "/api/v1/tasks", map[string]any{"title": " ", "kind": "general"}, nil); code != 400 {
		t.Fatalf("empty title: %d", code)
	}
	if code := c.do("POST", "/api/v1/tasks", map[string]any{"title": "x", "place": "worktree"}, nil); code != 400 {
		t.Fatalf("worktree task without project: %d", code)
	}
	if code := c.do("POST", "/api/v1/tasks", map[string]any{"title": "x", "review": "maybe"}, nil); code != 400 {
		t.Fatalf("unknown review: %d", code)
	}
	// Nothing is filled in by default (decision 0022): a task without its
	// attributes cannot be dispatched until they are set.
	if code := c.do("POST", "/api/v1/tasks/"+tk.ID+"/dispatch", map[string]any{}, nil); code != 400 {
		t.Fatalf("dispatch without attributes: %d", code)
	}
	if code := c.do("PATCH", "/api/v1/tasks/"+tk.ID, map[string]any{"place": "worktree", "review": "none"}, nil); code != 200 {
		t.Fatalf("set attributes: %d", code)
	}
	if code := c.do("POST", "/api/v1/tasks/"+tk.ID+"/dispatch", map[string]any{}, nil); code != 400 {
		t.Fatalf("dispatch without a start branch: %d", code)
	}
	if code := c.do("PATCH", "/api/v1/tasks/"+tk.ID, map[string]any{"base_branch": "main", "base_from": "local"}, &tk); code != 200 || tk.BaseBranch != "main" || tk.BaseFrom != "local" {
		t.Fatalf("set start: %d %+v", code, tk)
	}
	title, prio := "加一个蓝色按钮", 5
	if code := c.do("PATCH", "/api/v1/tasks/"+tk.ID, map[string]any{"title": title, "priority": prio, "permission_mode": "auto"}, &tk); code != 200 || tk.Title != title || tk.Priority != 5 || tk.PermissionMode != "auto" {
		t.Fatalf("patch: %d %+v", code, tk)
	}
	if code := c.do("PATCH", "/api/v1/tasks/"+tk.ID, map[string]any{"status": "running"}, nil); code != 409 {
		t.Fatalf("manual running: %d", code)
	}
	// Dispatching with the machine offline queues the task and puts it in the inbox.
	if code := c.do("POST", "/api/v1/tasks/"+tk.ID+"/dispatch", map[string]any{}, &tk); code != 200 || tk.Status != "queued" || !strings.Contains(tk.StatusReason, "离线") {
		t.Fatalf("dispatch: %d %+v", code, tk)
	}
	var inbox struct {
		Tasks     []store.Task
		Approvals []store.Approval
	}
	c.do("GET", "/api/v1/inbox", nil, &inbox)
	if len(inbox.Tasks) != 1 || inbox.Tasks[0].ID != tk.ID {
		t.Fatalf("inbox: %+v", inbox)
	}
	if code := c.do("DELETE", "/api/v1/projects/"+p.ID, nil, nil); code != 409 {
		t.Fatalf("delete a project with open tasks: %d", code)
	}
	if code := c.do("POST", "/api/v1/tasks/"+tk.ID+"/cancel", map[string]any{}, &tk); code != 200 || tk.Status != "cancelled" {
		t.Fatalf("cancel: %d %+v", code, tk)
	}
	var detail struct {
		Task store.Task
		Runs []store.Run
	}
	if code := c.do("GET", "/api/v1/tasks/"+tk.ID, nil, &detail); code != 200 || detail.Task.ID != tk.ID {
		t.Fatalf("get: %d", code)
	}
	var list struct{ Tasks []store.Task }
	c.do("GET", "/api/v1/tasks?status=cancelled", nil, &list)
	if len(list.Tasks) != 1 {
		t.Fatalf("list: %+v", list)
	}
	if code := c.do("DELETE", "/api/v1/projects/"+p.ID, nil, nil); code != 200 {
		t.Fatalf("delete project: %d", code)
	}
}

func TestSettingsAndPushEndpoints(t *testing.T) {
	c := setup(t)
	var s struct {
		DefaultMode string          `json:"default_mode"`
		Notify      map[string]bool `json:"notify"`
		Quiet       notify.Quiet    `json:"quiet"`
		Roles       map[string]string
	}
	c.do("GET", "/api/v1/settings", nil, &s)
	if s.DefaultMode != "default" || !s.Notify["approval"] || s.Roles["supervisor"] != "deepseek/x" {
		t.Fatalf("defaults: %+v", s)
	}
	body := map[string]any{"default_mode": "auto", "notify": map[string]bool{"report": false},
		"quiet": map[string]any{"enabled": true, "start": "23:00", "end": "07:30", "tz": "Asia/Shanghai"}}
	if code := c.do("PATCH", "/api/v1/settings", body, &s); code != 200 || s.DefaultMode != "auto" || s.Notify["report"] || !s.Notify["task_done"] || s.Quiet.Start != "23:00" {
		t.Fatalf("patch: %d %+v", code, s)
	}
	for _, bad := range []map[string]any{{"default_mode": "yolo"}, {"notify": map[string]bool{"spam": true}}, {"quiet": map[string]any{"start": "25:00", "end": "1:00"}}} {
		if code := c.do("PATCH", "/api/v1/settings", bad, nil); code != 400 {
			t.Fatalf("bad settings %v: %d", bad, code)
		}
	}
	var key struct{ Key string }
	if c.do("GET", "/api/v1/push/vapid-public-key", nil, &key); len(key.Key) < 80 {
		t.Fatalf("vapid key: %q", key.Key)
	}
	if code := c.do("POST", "/api/v1/push/subscriptions", map[string]any{"endpoint": "https://push.example/x", "keys": map[string]string{"p256dh": "bad", "auth": "bad"}}, nil); code != 400 {
		t.Fatalf("bad subscription keys: %d", code)
	}
}

package task_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func engine(t *testing.T) (*task.Engine, *eventlog.Log) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := eventlog.New(st, event.NewBus())
	nodes, _ := nodehub.New(log, t.TempDir())
	e := task.New(log, nodes)
	if err := e.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	log.EnsureMainThread(context.Background())
	return e, log
}

func call(t *testing.T, e *task.Engine, name string, args any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	for _, tl := range e.Tools() {
		if tl.Spec().Name == name {
			return tl.Call(tool.WithTurn(context.Background(), tool.Turn{}), raw)
		}
	}
	t.Fatalf("no tool %s", name)
	return "", nil
}

func TestSeedAndPersonas(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	ps, _ := log.Store.Personas(ctx)
	if len(ps) != len(task.BuiltinPersonas) || ps[0].Name != "后端" || !ps[0].Builtin {
		t.Fatalf("personas: %+v", ps)
	}
	// Deleting a built-in persona is not undone by the next start.
	if err := e.DeletePersona(ctx, "assistant", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	e.Seed(ctx)
	if ps, _ = log.Store.Personas(ctx); len(ps) != len(task.BuiltinPersonas)-1 {
		t.Fatalf("after reseed: %d", len(ps))
	}
	if _, err := log.Store.Project(ctx, "misc"); err != nil {
		t.Fatal("misc project missing")
	}
	// A task without a project belongs to misc; a persona is checked.
	tk, err := e.Create(ctx, task.Input{Title: "查磁盘", Persona: "ops", Actor: event.ActorUser})
	if err != nil || tk.ProjectID != "misc" || tk.Kind != "general" || tk.PersonaID != "ops" {
		t.Fatalf("task %+v %v", tk, err)
	}
	if _, err := e.Create(ctx, task.Input{Title: "x", Persona: "nobody", Actor: event.ActorUser}); err == nil {
		t.Fatal("unknown persona accepted")
	}
	if _, err := e.Create(ctx, task.Input{Title: "x", Kind: "ops", Actor: event.ActorUser}); err == nil {
		t.Fatal("an ops task needs a project")
	}
	if _, err := e.PutPersona(ctx, task.PersonaInput{Name: "后端"}, event.ActorUser); err == nil {
		t.Fatal("duplicate persona name accepted")
	}
}

func TestProjectTools(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	log.Append(ctx,
		event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{NodeID: "srv", Name: "服务器"}),
		event.New(event.NodeRole, event.ActorSystem, "", event.NodeRolePayload{NodeID: "srv", Role: "server"}),
		event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{NodeID: "mac", Name: "Mac"}))

	out, err := call(t, e, "project_put", map[string]any{"name": "blog", "repo_url": "git@github.com:me/blog.git", "default_persona": "前端",
		"deploy": map[string]any{"machine": "服务器", "path": "/srv/blog", "command": "./deploy.sh", "auto_on_merge": true}})
	if err != nil || !strings.Contains(out, "blog") {
		t.Fatalf("%s %v", out, err)
	}
	p, _ := e.Log.Store.ProjectByKey(ctx, "github.com/me/blog")
	if p.DefaultPersona != "frontend" || p.Deploy == nil || p.Deploy.NodeID != "srv" || !p.Deploy.AutoOnMerge {
		t.Fatalf("project %+v", p)
	}
	// The same repository cannot be registered twice; a dev machine is no deploy target.
	if _, err := call(t, e, "project_put", map[string]any{"name": "blog2", "repo_url": "https://github.com/me/blog"}); err == nil {
		t.Fatal("duplicate repository accepted")
	}
	if _, err := call(t, e, "project_put", map[string]any{"id": "blog", "deploy": map[string]any{"machine": "Mac", "path": "/x", "command": "y"}}); err == nil {
		t.Fatal("a dev machine became a deploy target")
	}
	// Creating needs a valid name and an online machine.
	if _, err := call(t, e, "project_create", map[string]any{"name": "My App"}); err == nil || !strings.Contains(err.Error(), "小写") {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := call(t, e, "project_create", map[string]any{"name": "my-app"}); err == nil || !strings.Contains(err.Error(), "没有机器") {
		t.Fatalf("no machine: %v", err)
	}
	if _, err := call(t, e, "project_clone", map[string]any{"project": "blog"}); err == nil {
		t.Fatal("clone with no machine online")
	}
	// The supervisor's environment lists personas and the deploy target.
	env := strings.Join(e.Environment(ctx, ""), "\n")
	if !strings.Contains(env, "岗位 前端") || !strings.Contains(env, "部署到 服务器:/srv/blog") || !strings.Contains(env, "wokspace") {
		t.Fatalf("environment:\n%s", env)
	}
	// task_create takes a persona by name and an ops kind.
	out, err = call(t, e, "task_create", map[string]any{"title": "部署", "detail": "d", "project": "blog", "kind": "ops", "persona": "运维", "dispatch": false})
	if err != nil || !strings.Contains(out, "部署") {
		t.Fatalf("%s %v", out, err)
	}
	ts, _ := e.Log.Store.Tasks(ctx, store.TaskFilter{ProjectID: p.ID})
	if len(ts) != 1 || ts[0].Kind != "ops" || ts[0].PersonaID != "ops" {
		t.Fatalf("tasks %+v", ts)
	}
	if got := e.DescribeContext(ctx, event.MessageContext{ProjectID: p.ID}); !strings.Contains(got, "blog") {
		t.Fatalf("context: %s", got)
	}
}

func TestDeployedSince(t *testing.T) {
	ts := []store.Task{
		{ProjectID: "p", Kind: "code", CreatedAt: 10},
		{ProjectID: "p", Kind: "ops", CreatedAt: 5, Status: "done"},
		{ProjectID: "q", Kind: "ops", CreatedAt: 20, Status: "done"},
		{ProjectID: "p", Kind: "ops", CreatedAt: 30, Status: "cancelled"},
	}
	if task.DeployedSince(ts, "p", 10) {
		t.Fatal("no deploy of p after 10")
	}
	ts = append(ts, store.Task{ProjectID: "p", Kind: "ops", CreatedAt: 12, Status: "running"})
	if !task.DeployedSince(ts, "p", 10) {
		t.Fatal("a deploy under way counts")
	}
}

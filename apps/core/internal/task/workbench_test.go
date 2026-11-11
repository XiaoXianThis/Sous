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

func TestCreateValidatesDispatchBeforePersisting(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	name := "dispatch-check"
	p, err := e.PutProject(ctx, task.ProjectInput{Name: &name}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []task.Input{
		{Title: "missing place", Dispatch: true},
		{Title: "missing branch", ProjectID: p.ID, Place: event.PlaceWorktree, Dispatch: true},
		{Title: "blank branch", ProjectID: p.ID, Place: event.PlaceWorktree, BaseBranch: "  ", BaseFrom: "local", Dispatch: true},
	} {
		if _, err := e.Create(ctx, in); err == nil {
			t.Fatalf("incomplete request succeeded: %+v", in)
		}
	}
	if tasks, err := log.Store.Tasks(ctx, store.TaskFilter{}); err != nil || len(tasks) != 0 {
		t.Fatalf("failed requests left tasks behind: %+v, %v", tasks, err)
	}
	// Planning an incomplete task is still allowed, and a valid dispatch
	// with no online machine is durably queued rather than reported failed.
	if _, err := e.Create(ctx, task.Input{Title: "plan"}); err != nil {
		t.Fatal(err)
	}
	queued, err := e.Create(ctx, task.Input{Title: "wait", Place: event.PlaceScratch, Dispatch: true})
	if err != nil || queued.Status != event.TaskQueued || queued.StatusReason == "" {
		t.Fatalf("offline dispatch: %+v, %v", queued, err)
	}
}

func TestSeedAndPersonas(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	ps, _ := log.Store.Personas(ctx)
	// Decision 0024: the only built-in preset is the reviewer.
	if len(ps) != 1 || ps[0].ID != "reviewer" || ps[0].Name != "验收" || !ps[0].Builtin {
		t.Fatalf("personas: %+v", ps)
	}
	// Deleting a built-in preset is not undone by the next start.
	if err := e.DeletePersona(ctx, "reviewer", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	e.Seed(ctx)
	if ps, _ = log.Store.Personas(ctx); len(ps) != 0 {
		t.Fatalf("after reseed: %d", len(ps))
	}
	ops, err := e.PutPersona(ctx, task.PersonaInput{Name: "后端", Agent: "codex", Options: map[string]string{"model": "gpt-6.1-terra"}, Brief: "快速"}, event.ActorUser)
	if err != nil || ops.Glyph != "后" {
		t.Fatalf("preset %+v %v", ops, err)
	}
	if _, err := log.Store.Project(ctx, "misc"); err != nil {
		t.Fatal("misc project missing")
	}
	// A task without a project belongs to misc; a persona is checked.
	tk, err := e.Create(ctx, task.Input{Title: "查磁盘", Persona: ops.ID, Place: event.PlaceScratch, Actor: event.ActorUser})
	if err != nil || tk.ProjectID != "misc" || tk.Kind != "general" || tk.PersonaID != ops.ID || tk.Place != "scratch" || tk.Review != "none" {
		t.Fatalf("task %+v %v", tk, err)
	}
	if _, err := e.Create(ctx, task.Input{Title: "x", Persona: "nobody", Actor: event.ActorUser}); err == nil {
		t.Fatal("unknown persona accepted")
	}
	if _, err := e.Create(ctx, task.Input{Title: "x", Kind: "ops", Place: event.PlaceInPlace, Actor: event.ActorUser}); err == nil {
		t.Fatal("an in-place task needs a project")
	}
	if _, err := e.PutPersona(ctx, task.PersonaInput{Name: "后端"}, event.ActorUser); err == nil {
		t.Fatal("duplicate persona name accepted")
	}
}

func TestProjectTools(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	fe, err := e.PutPersona(ctx, task.PersonaInput{Name: "前端"}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	log.Append(ctx,
		event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{NodeID: "srv", Name: "服务器"}),
		event.New(event.NodeRole, event.ActorSystem, "", event.NodeRolePayload{NodeID: "srv", Role: "server"}),
		event.New(event.NodePaired, event.ActorUser, "", event.NodePayload{NodeID: "mac", Name: "Mac"}))

	out, err := call(t, e, "project_put", map[string]any{"name": "blog", "repo_url": "git@github.com:me/blog.git", "default_persona": "前端",
		"deploy": map[string]any{"machine": "服务器", "path": "/srv/blog", "command": "./deploy.sh", "auto_on_merge": true}})
	if err == nil || !strings.Contains(err.Error(), "部署分支") {
		t.Fatalf("automatic deploys without a deploy branch: %s %v", out, err)
	}
	out, err = call(t, e, "project_put", map[string]any{"name": "blog", "repo_url": "git@github.com:me/blog.git", "default_persona": "前端",
		"deploy": map[string]any{"machine": "服务器", "path": "/srv/blog", "command": "./deploy.sh", "branch": "release", "auto_on_merge": true}})
	if err != nil || !strings.Contains(out, "blog") {
		t.Fatalf("%s %v", out, err)
	}
	p, _ := e.Log.Store.ProjectByKey(ctx, "github.com/me/blog")
	if p.DefaultPersona != fe.ID || p.Deploy == nil || p.Deploy.NodeID != "srv" || !p.Deploy.AutoOnMerge || p.Deploy.Branch != "release" {
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
	// No default structure: the supervisor says which (decision 0022).
	if _, err := call(t, e, "project_create", map[string]any{"name": "my-app"}); err == nil || !strings.Contains(err.Error(), "结构") {
		t.Fatalf("no layout: %v", err)
	}
	if _, err := call(t, e, "project_create", map[string]any{"name": "my-app", "layout": "wokspace"}); err == nil || !strings.Contains(err.Error(), "没有机器") {
		t.Fatalf("no machine: %v", err)
	}
	if _, err := call(t, e, "project_clone", map[string]any{"project": "blog"}); err == nil {
		t.Fatal("clone with no machine online")
	}
	// The supervisor's environment lists personas and the deploy target.
	env := strings.Join(e.Environment(ctx, ""), "\n")
	if !strings.Contains(env, "预设 前端") || !strings.Contains(env, "部署到 服务器:/srv/blog") || !strings.Contains(env, "release 有新提交时自动部署") {
		t.Fatalf("environment:\n%s", env)
	}
	if strings.Contains(env, "默认使用 wokspace") {
		t.Fatalf("the environment still presets a structure:\n%s", env)
	}
	// task_create takes a preset by name, a model over it and an ops kind;
	// without review it is not reviewed (decision 0024).
	ops, _ := e.PutPersona(ctx, task.PersonaInput{Name: "运维", Agent: "claude"}, event.ActorUser)
	out, err = call(t, e, "task_create", map[string]any{"title": "部署", "detail": "d", "project": "blog", "kind": "ops", "preset": "运维",
		"model": "opus-5.5", "effort": "high", "place": "in_place", "dispatch": false})
	if err != nil || !strings.Contains(out, "部署") || !strings.Contains(out, "模型 opus-5.5") {
		t.Fatalf("%s %v", out, err)
	}
	ts, _ := e.Log.Store.Tasks(ctx, store.TaskFilter{ProjectID: p.ID})
	if len(ts) != 1 || ts[0].Kind != "ops" || ts[0].PersonaID != ops.ID || ts[0].Place != "in_place" || ts[0].Review != "none" ||
		ts[0].Options["model"] != "opus-5.5" || ts[0].Options["effort"] != "high" {
		t.Fatalf("tasks %+v", ts)
	}
	// task_update changes one setting and keeps the other; "" goes back
	// to the preset's.
	if _, err := call(t, e, "task_update", map[string]any{"id": ts[0].ID, "model": ""}); err != nil {
		t.Fatal(err)
	}
	if tk, _ := e.Log.Store.Task(ctx, ts[0].ID); tk.Options["model"] != "" || tk.Options["effort"] != "high" {
		t.Fatalf("options after update: %+v", tk.Options)
	}
	// The environment says where a preset's agent can run.
	if env := strings.Join(e.Environment(ctx, ""), "\n"); !strings.Contains(env, "预设 运维") || !strings.Contains(env, "现在没有电脑能用它") {
		t.Fatalf("environment:\n%s", env)
	}
	if got := e.DescribeContext(ctx, event.MessageContext{ProjectID: p.ID}); !strings.Contains(got, "blog") {
		t.Fatalf("context: %s", got)
	}
}

func TestArchiveProject(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	if _, err := call(t, e, "project_put", map[string]any{"name": "old-site", "repo_url": "git@github.com:me/old-site.git"}); err != nil {
		t.Fatal(err)
	}
	p, _ := log.Store.ProjectByKey(ctx, "github.com/me/old-site")
	if p.Archived() || p.TouchedAt < p.UpdatedAt {
		t.Fatalf("new project %+v", p)
	}
	if _, err := e.ArchiveProject(ctx, "misc", true, event.ActorUser); err == nil {
		t.Fatal("misc archived")
	}
	// The archived state survives a project.put.
	if out, err := call(t, e, "project_archive", map[string]any{"project": "old-site", "archived": true}); err != nil || !strings.Contains(out, "已归档") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := call(t, e, "project_put", map[string]any{"id": "old-site", "notes": "旧网站"}); err != nil {
		t.Fatal(err)
	}
	if p, _ = log.Store.Project(ctx, p.ID); !p.Archived() || p.Notes != "旧网站" {
		t.Fatalf("after put %+v", p)
	}
	// The supervisor sees it only as an archived project.
	env := strings.Join(e.Environment(ctx, ""), "\n")
	if strings.Contains(env, "项目 old-site（") || !strings.Contains(env, "已归档的项目") {
		t.Fatalf("environment:\n%s", env)
	}
	// A new task brings it back; its change moves touched_at.
	tk, err := e.Create(ctx, task.Input{Title: "改首页", ProjectID: p.ID, Actor: event.ActorUser,
		Place: event.PlaceWorktree, Review: event.ReviewNone, BaseBranch: "main", BaseFrom: event.BaseLocal})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ = log.Store.Project(ctx, p.ID); p.Archived() || p.TouchedAt < tk.UpdatedAt {
		t.Fatalf("after task %+v", p)
	}
	// A project with work in flight stays active.
	if _, err := e.Dispatch(ctx, tk.ID, "", "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ArchiveProject(ctx, p.ID, true, event.ActorUser); err == nil || !strings.Contains(err.Error(), "正在执行") {
		t.Fatalf("archived with a queued task: %v", err)
	}
}

func TestProjectChatScope(t *testing.T) {
	e, log := engine(t)
	ctx := context.Background()
	for _, n := range []string{"blog", "shop"} {
		if _, err := call(t, e, "project_put", map[string]any{"name": n, "repo_url": "git@github.com:me/" + n + ".git"}); err != nil {
			t.Fatal(err)
		}
	}
	blog, _ := log.Store.ProjectByKey(ctx, "github.com/me/blog")
	shop, _ := log.Store.ProjectByKey(ctx, "github.com/me/shop")
	other, err := e.Create(ctx, task.Input{Title: "shop 的活", ProjectID: shop.ID, Actor: event.ActorUser})
	if err != nil {
		t.Fatal(err)
	}

	ev := event.New(event.ThreadCreated, event.ActorUser, "", event.ThreadCreatedPayload{Title: "blog", Kind: "project", ProjectID: blog.ID})
	ev.ThreadID = ev.ID
	if _, err := log.Append(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if ts, _ := log.Store.Threads(ctx); len(ts) != 1 {
		t.Fatalf("project chats leak into the thread list: %+v", ts)
	}
	if ts, _ := log.Store.ProjectThreads(ctx, blog.ID); len(ts) != 1 || ts[0].ProjectID != blog.ID {
		t.Fatalf("project threads %+v", ts)
	}

	scoped := tool.WithTurn(ctx, tool.Turn{ThreadID: ev.ThreadID, ProjectID: blog.ID})
	run := func(name string, args any) (string, error) {
		raw, _ := json.Marshal(args)
		for _, tl := range e.Tools() {
			if tl.Spec().Name == name {
				return tl.Call(scoped, raw)
			}
		}
		t.Fatalf("no tool %s", name)
		return "", nil
	}
	// A task without a project goes to the chat's project.
	if _, err := run("task_create", map[string]any{"title": "改首页", "detail": "d", "dispatch": false}); err != nil {
		t.Fatal(err)
	}
	if ts, _ := log.Store.Tasks(ctx, store.TaskFilter{ProjectID: blog.ID}); len(ts) != 1 || ts[0].Kind != "code" {
		t.Fatalf("blog tasks %+v", ts)
	}
	// Other projects and their tasks are out of reach.
	for name, args := range map[string]any{
		"task_create":     map[string]any{"title": "x", "detail": "d", "project": "shop"},
		"task_update":     map[string]any{"id": other.ID, "title": "y"},
		"task_cancel":     map[string]any{"id": other.ID},
		"project_create":  map[string]any{"name": "new-app"},
		"project_archive": map[string]any{"project": "shop", "archived": true},
	} {
		if _, err := run(name, args); err == nil || !strings.Contains(err.Error(), "项目对话") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if out, _ := run("task_list", map[string]any{}); strings.Contains(out, "shop 的活") {
		t.Fatalf("task_list shows another project: %s", out)
	}
	env := strings.Join(e.Environment(ctx, ev.ThreadID), "\n")
	if !strings.Contains(env, "项目 blog") || strings.Contains(env, "项目 shop") || !strings.Contains(env, "改首页") {
		t.Fatalf("scoped environment:\n%s", env)
	}
}

// The supervisor's tools have no defaults where decision 0022 says the
// supervisor decides: the attributes are required in the schema, and the
// descriptions never name a default branch or structure.
func TestToolsNameTheirChoices(t *testing.T) {
	e, _ := engine(t)
	required := map[string][]string{
		"task_create":    {"place"}, // decision 0024: review is optional, none unless asked
		"git_merge":      {"task", "into"},
		"git_push":       {"branch"},
		"git_pr":         {"task", "base"},
		"project_create": {"layout"},
		"consult":        {"question"},
		"project_read":   {"op"},
	}
	seen := map[string]bool{}
	for _, tl := range e.Tools() {
		spec := tl.Spec()
		var schema struct{ Required []string }
		if err := json.Unmarshal(spec.Schema, &schema); err != nil {
			t.Fatalf("%s: %v", spec.Name, err)
		}
		for _, want := range required[spec.Name] {
			found := false
			for _, r := range schema.Required {
				found = found || r == want
			}
			if !found {
				t.Errorf("%s does not require %s", spec.Name, want)
			}
		}
		if strings.Contains(spec.Description, "默认 wokspace") || strings.Contains(spec.Description, "默认 main") && !strings.Contains(spec.Description, "不要默认 main") {
			t.Errorf("%s presets something: %s", spec.Name, spec.Description)
		}
		seen[spec.Name] = true
	}
	for name := range required {
		if !seen[name] {
			t.Errorf("no tool %s", name)
		}
	}
	for _, name := range []string{"git_status", "git_commit", "git_undo", "git_discard", "task_restore", "task_review", "task_verdict"} {
		if !seen[name] {
			t.Errorf("no tool %s", name)
		}
	}
}

// machine_look is a glance: a few per turn, then the supervisor is told
// to hand the rest to a task (decision 0024).
func TestMachineLookIsLimitedPerTurn(t *testing.T) {
	e, _ := engine(t)
	var look tool.Tool
	for _, tl := range e.Tools() {
		if tl.Spec().Name == "machine_look" {
			look = tl
		}
	}
	if look == nil {
		t.Fatal("no machine_look")
	}
	turn := tool.WithTurn(context.Background(), tool.Turn{ThreadID: "th", ReplyTo: 7})
	for i := 0; i < 4; i++ {
		if _, err := look.Call(turn, json.RawMessage(`{"command":"df -h"}`)); err == nil || strings.Contains(err.Error(), "这一轮已经看了") {
			t.Fatalf("look %d: %v", i, err)
		}
	}
	if _, err := look.Call(turn, json.RawMessage(`{"command":"df -h"}`)); err == nil || !strings.Contains(err.Error(), "这一轮已经看了") {
		t.Fatalf("the fifth look: %v", err)
	}
	next := tool.WithTurn(context.Background(), tool.Turn{ThreadID: "th", ReplyTo: 8})
	if _, err := look.Call(next, json.RawMessage(`{"command":"df -h"}`)); err == nil || strings.Contains(err.Error(), "这一轮已经看了") {
		t.Fatalf("a new turn starts over: %v", err)
	}
}

func TestDescribeContextIgnoresNoteIDs(t *testing.T) {
	e, _ := engine(t)
	if got := e.DescribeContext(context.Background(), event.MessageContext{NoteIDs: []string{"secret-note-id"}}); got != "" {
		t.Fatalf("note context leaked: %q", got)
	}
}

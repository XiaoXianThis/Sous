package node_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// fakeWok puts a wok on PATH that creates the wokspace layout.
func fakeWok(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
version) echo "wok 0.3.0";;
init) mkdir -p "$2" && cd "$2" && git init -q && mkdir -p apps build docs work && echo "# AGENTS" > AGENTS.md;;
check) [ -f AGENTS.md ] && [ -d apps ];;
esac
`
	os.WriteFile(filepath.Join(bin, "wok"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (te *taskEnv) setConfig(change func(*node.Config)) {
	te.t.Helper()
	change(&te.cfg)
	if err := node.SaveConfig(te.root, te.cfg); err != nil {
		te.t.Fatal(err)
	}
}

func (te *taskEnv) approveRun(runID string) store.Approval {
	te.t.Helper()
	a := te.pendingApproval(runID)
	if err := te.hub.Decide(context.Background(), a.ID, a.Options[0].OptionID, ""); err != nil {
		te.t.Fatal(err)
	}
	te.waitFor("the approval to be decided", func() bool {
		as, _ := te.log.Store.Approvals(context.Background(), "pending", runID)
		for _, x := range as {
			if x.ID == a.ID {
				return false
			}
		}
		return true
	})
	return a
}

func TestWorkspacesProjectsAndSetup(t *testing.T) {
	te := newTaskEnv(t)
	fakeWok(t)
	ctx := context.Background()
	exec.Command("git", "-C", te.repo, "push", "-q", "origin", "HEAD:refs/heads/main").Run()
	exec.Command("git", "-C", te.origin, "symbolic-ref", "HEAD", "refs/heads/main").Run()

	ws := t.TempDir()
	scanned := filepath.Join(ws, "scanned")
	os.MkdirAll(scanned, 0o755)
	exec.Command("git", "-C", scanned, "init", "-q").Run()
	exec.Command("git", "-C", scanned, "remote", "add", "origin", "git@github.com:u/Scanned.git").Run()
	te.setConfig(func(c *node.Config) { c.Workspaces = []string{ws} })

	name, url := "scanned", "https://github.com/u/Scanned"
	p2, err := te.tasks.PutProject(ctx, task.ProjectInput{Name: &name, RepoURL: &url}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	defer te.startDaemon("write")()

	// The repository in the workspace becomes the project's place on this machine.
	te.waitFor("the scanned place", func() bool {
		p, _ := te.log.Store.Project(ctx, p2.ID)
		return len(p.Nodes) == 1 && p.Nodes[0].Path == scanned && p.Nodes[0].Source == "scan"
	})
	n, _ := te.log.Store.Node(ctx, te.cfg.NodeID)
	if n.Role != "dev" || n.Wok == "" || len(n.Workspaces) != 1 {
		t.Fatalf("node: %+v", n)
	}

	// A new project is a wokspace by default. wok only writes inside the
	// workspace, so the policy allows it (creating a GitHub repository is
	// git-remote and asks).
	p3, run, err := te.tasks.CreateProject(ctx, task.SetupInput{Name: "new-app", ThreadID: te.main, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	if r := te.ended(run.ID); r.Status != event.RunStatusSucceeded {
		t.Fatalf("create run: %+v", r)
	}
	te.waitFor("the new project's place", func() bool {
		p, _ := te.log.Store.Project(ctx, p3.ID)
		return len(p.Nodes) == 1 && p.Nodes[0].Path == filepath.Join(ws, "new-app") && p.Layout == "wokspace"
	})
	if _, err := os.Stat(filepath.Join(ws, "new-app", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	// Cloning puts a registered project into the workspace.
	origin := te.origin
	p4name := "cloned"
	p4, err := te.tasks.PutProject(ctx, task.ProjectInput{Name: &p4name, RepoURL: &origin}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	crun, err := te.tasks.CloneProject(ctx, task.SetupInput{ProjectID: p4.ID, Actor: event.ActorUser})
	if err != nil {
		t.Fatal(err)
	}
	if a := te.approveRun(crun.ID); a.Review != "user" || !strings.Contains(a.Detail, "git clone") {
		t.Fatalf("clone approval: %+v", a)
	}
	if r := te.ended(crun.ID); r.Status != event.RunStatusSucceeded {
		t.Fatalf("clone run: %+v", r)
	}
	te.waitFor("the cloned project's place", func() bool {
		p, _ := te.log.Store.Project(ctx, p4.ID)
		return len(p.Nodes) == 1 && p.Nodes[0].Path == filepath.Join(ws, "origin")
	})
	if _, err := os.Stat(filepath.Join(ws, "origin", "README.md")); err != nil {
		t.Fatal(err)
	}
	te.waitFor("the setup reports", func() bool {
		msgs, _ := te.log.Store.Messages(ctx, te.main, 0, 50)
		n := 0
		for _, m := range msgs {
			if m.Kind == event.KindReport && (strings.Contains(m.Content, "已经创建到") || strings.Contains(m.Content, "已经 clone 到")) {
				n++
			}
		}
		return n == 2
	})
}

func TestOpsTaskOnServer(t *testing.T) {
	te := newTaskEnv(t)
	ctx := context.Background()
	te.setConfig(func(c *node.Config) { c.Role = "server" })
	prompts := filepath.Join(t.TempDir(), "prompts.log")
	_, stop := te.startDaemonWith("write", "FAKE_ACP_OPTIONS=1", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	te.waitFor("the server role", func() bool {
		n, _ := te.log.Store.Node(ctx, te.cfg.NodeID)
		return n.Role == "server"
	})

	// A server takes no code tasks.
	code, _ := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "改代码", Dispatch: true, Actor: event.ActorUser})
	if c := te.task(code.ID); c.Status != event.TaskQueued || !strings.Contains(c.StatusReason, "服务器") {
		t.Fatalf("code task on a server: %+v", c)
	}
	te.tasks.Cancel(ctx, code.ID, "", event.ActorUser)

	// An ops task runs in place with the ops persona, in the default mode
	// even when asked for open, and its command goes to the user.
	persona, err := te.tasks.PutPersona(ctx, task.PersonaInput{Name: "部署员", Agent: "fake", Options: map[string]string{"model": "smart"},
		Brief: "按脚本部署", Kinds: []string{"ops"}}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	dep := &event.Deploy{NodeID: te.cfg.NodeID, Path: te.repo, Command: "./deploy.sh", Health: "curl -f localhost"}
	if _, err := te.tasks.PutProject(ctx, task.ProjectInput{ID: "p1", Deploy: &dep}, event.ActorUser); err != nil {
		t.Fatal(err)
	}
	ops, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Kind: "ops", Title: "部署", Persona: persona.ID,
		Mode: "open", Dispatch: true, Actor: event.ActorUser})
	if err != nil {
		t.Fatal(err)
	}
	a := te.approveNext(ops.ID)
	if a.Review != "user" {
		t.Fatalf("approval on a server: %+v", a)
	}
	done := te.waitStatus(ops.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked)
	if done.Status != event.TaskDone {
		t.Fatalf("ops task: %+v", done)
	}
	runs := te.runs(ops.ID)
	if len(runs) != 1 || runs[0].Kind != "ops" || runs[0].Workdir != te.repo || runs[0].Persona != "部署员" || runs[0].Options["model"] != "smart" {
		t.Fatalf("runs: %+v", runs)
	}
	if len(te.reviewer.requests()) != 0 {
		t.Fatal("ops tasks are not reviewed")
	}
	log, _ := os.ReadFile(prompts)
	if !strings.Contains(string(log), "set model=smart") || !strings.Contains(string(log), "【你的岗位：部署员】按脚本部署") ||
		!strings.Contains(string(log), "./deploy.sh") {
		t.Fatalf("agent saw: %s", log)
	}
	if _, err := os.Stat(filepath.Join(te.root, "worktrees")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(te.root, "worktrees"))
		if len(entries) > 0 {
			t.Fatal("an ops run must not create a worktree")
		}
	}
}

func TestDeployWhenPRMerged(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "Add hello", Detail: "创建 hello.txt", Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	te.approveNext(tk.ID)
	te.approveNext(tk.ID)
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}

	// A server (offline here) is the deploy target, deploying on merge.
	te.log.Append(ctx,
		event.New(event.NodePaired, event.ActorSystem, "", event.NodePayload{NodeID: "srv", Name: "服务器", OS: "linux", Arch: "amd64"}),
		event.New(event.NodeRole, event.ActorSystem, "", event.NodeRolePayload{NodeID: "srv", Role: "server"}))
	dep := &event.Deploy{NodeID: "srv", Path: "/srv/demo", Command: "./deploy.sh", AutoOnMerge: true}
	if _, err := te.tasks.PutProject(ctx, task.ProjectInput{ID: "p1", Deploy: &dep}, event.ActorUser); err != nil {
		t.Fatal(err)
	}

	// While the PR is open nothing happens.
	te.tasks.CheckMerges(ctx)
	if ops, _ := te.log.Store.Tasks(ctx, store.TaskFilter{ProjectID: "p1"}); len(ops) != 1 {
		t.Fatalf("tasks before the merge: %d", len(ops))
	}
	// gh now says merged: one ops task, once.
	gh, _ := exec.LookPath("gh")
	os.WriteFile(gh, []byte("#!/bin/sh\necho '{\"state\":\"MERGED\",\"mergedAt\":\"2026-11-04T00:00:00Z\"}'\n"), 0o755)
	te.tasks.CheckMerges(ctx)
	te.tasks.CheckMerges(ctx)
	all, _ := te.log.Store.Tasks(ctx, store.TaskFilter{ProjectID: "p1"})
	var ops []store.Task
	for _, x := range all {
		if x.Kind == "ops" {
			ops = append(ops, x)
		}
	}
	if len(ops) != 1 || ops[0].Status != event.TaskQueued || !strings.Contains(ops[0].Detail, "pull/1") || ops[0].PersonaID != "ops" {
		t.Fatalf("ops tasks: %+v", ops)
	}
	if r := te.task(ops[0].ID).StatusReason; !strings.Contains(r, "离线") {
		t.Fatalf("waits for the server: %s", r)
	}
}

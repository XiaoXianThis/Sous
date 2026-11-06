package node_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// The end-to-end review of 2026-11 (work/specs/e2e-review-2026-11): runs that
// did nothing, refusals, slots and deploys.

// An agent out of its plan says so and ends its turn "normally": the task
// fails with the reason in plain words instead of being done (4.1, 4.14).
func TestQuotaTurnFailsTheTask(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("quota")()
	tk, err := te.tasks.Create(context.Background(), te.general(task.Input{ThreadID: te.main, Title: "清理日志", Dispatch: true, Review: event.ReviewNone}))
	if err != nil {
		t.Fatal(err)
	}
	failed := te.waitStatus(tk.ID, event.TaskFailed, event.TaskDone, event.TaskBlocked)
	if failed.Status != event.TaskFailed || !strings.Contains(failed.StatusReason, "额度用完") {
		t.Fatalf("task: %+v", failed)
	}
	runs := te.runs(tk.ID)
	if len(runs) != 1 || runs[0].ErrorKind != proto.ErrAgentQuota || !strings.Contains(runs[0].Error, "Upgrade your plan") {
		t.Fatalf("run: %+v", runs)
	}
}

// A turn without a tool call, a change or a real answer is not done: the
// task stops and the supervisor gets a follow-up to decide (4.1).
func TestNoActionTurnBlocksTheTask(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("noop")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.general(task.Input{ThreadID: te.main, Title: "部署", Dispatch: true, Review: event.ReviewModel, OnFail: event.OnFailRework}))
	blocked := te.waitStatus(tk.ID, event.TaskBlocked, event.TaskDone, event.TaskFailed)
	if blocked.Status != event.TaskBlocked || !strings.Contains(blocked.StatusReason, "没做任何操作") {
		t.Fatalf("task: %+v", blocked)
	}
	if n := len(te.reviewer.requests()); n != 0 {
		t.Fatalf("a run that did nothing went to the reviewer (%d calls)", n)
	}
	if runs := te.runs(tk.ID); len(runs) != 1 || runs[0].Outcome != proto.OutcomeNoAction {
		t.Fatalf("runs: %+v", runs)
	}
	te.waitFor("the follow-up", func() bool {
		evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
		for _, ev := range evs {
			var p event.TriggerPayload
			if ev.Type == event.TriggerFired && ev.Decode(&p) == nil && strings.HasPrefix(p.Key, "no-action:") {
				return true
			}
		}
		return false
	})
}

// rejectNext refuses the task's next approval that waits for the user.
func (te *taskEnv) rejectNext(taskID string) store.Approval {
	te.t.Helper()
	var a store.Approval
	te.waitFor("an approval to refuse", func() bool {
		as, _ := te.log.Store.Approvals(context.Background(), "pending", "")
		for _, x := range as {
			if x.TaskID == taskID && x.Review == "user" {
				a = x
				return true
			}
		}
		return false
	})
	for _, o := range a.Options {
		if strings.HasPrefix(o.Kind, "reject") {
			if err := te.hub.Decide(context.Background(), a.ID, o.OptionID, "不要动这个"); err != nil {
				te.t.Fatal(err)
			}
			return a
		}
	}
	te.t.Fatalf("no reject option: %+v", a.Options)
	return a
}

// A refusal is not a failed attempt to retry (4.2). A run whose operation
// was refused and that changed nothing stops, done by no one and reviewed by
// no one, even without a review; a later run asking the very same thing gets
// the user's answer without asking again.
func TestRefusalWithoutChangesStops(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "写 hello", Dispatch: true, Review: event.ReviewNone}))
	first := te.rejectNext(tk.ID)
	blocked := te.waitStatus(tk.ID, event.TaskBlocked, event.TaskDone)
	if blocked.Status != event.TaskBlocked || !strings.Contains(blocked.StatusReason, "没有留下改动") || !strings.Contains(blocked.StatusReason, "不要动这个") {
		t.Fatalf("task: %+v", blocked)
	}
	if n := len(te.reviewer.requests()); n != 0 {
		t.Fatalf("reviewed a refused run (%d calls)", n)
	}

	// The user retries: the same request is refused as the user refused it.
	if _, err := te.tasks.Dispatch(ctx, tk.ID, "", "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	te.waitFor("the second run to end", func() bool {
		return len(te.runs(tk.ID)) == 2 && te.runs(tk.ID)[1].Status != event.RunStatusRunning && te.runs(tk.ID)[1].Status != event.RunStatusQueued
	})
	te.waitStatus(tk.ID, event.TaskBlocked, event.TaskDone)
	as, _ := te.log.Store.TaskApprovals(ctx, tk.ID)
	var again *store.Approval
	for i := range as {
		if as[i].ID != first.ID {
			again = &as[i]
		}
	}
	if again == nil || again.DecidedBy != event.ActorApprover || !strings.HasPrefix(again.OptionID, "reject") {
		t.Fatalf("the second request: %+v", again)
	}
}

// A round where something was refused and the agent changed files another
// way goes to review with the refusals; failing it stops instead of
// reworking (4.2).
func TestRefusalStopsRework(t *testing.T) {
	te := newTaskEnv(t, `{"pass":false,"reason":"写法不对"}`)
	defer te.startDaemon("reject")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "写 hello", Dispatch: true, OnFail: event.OnFailRework}))
	te.rejectNext(tk.ID)
	blocked := te.waitStatus(tk.ID, event.TaskBlocked, event.TaskDone)
	if blocked.Status != event.TaskBlocked || !strings.Contains(blocked.StatusReason, "没有自动返工") {
		t.Fatalf("task: %+v", blocked)
	}
	runs := te.runs(tk.ID)
	if len(runs) != 1 || len(runs[0].Refused) == 0 || runs[0].DiffStat == "" {
		t.Fatalf("runs: %+v", runs)
	}
	if reqs := te.reviewer.requests(); len(reqs) != 1 || !strings.Contains(reqs[0].Messages[0].PlainText(), "被拒绝的操作") {
		t.Fatalf("the reviewer was not told about the refusal: %d calls", len(reqs))
	}
}

// A run waiting for the user gives its slot up: on a one-slot machine a
// second task starts meanwhile (end-to-end review, beyond 4.3).
func TestWaitingForTheUserFreesTheSlot(t *testing.T) {
	te := newTaskEnv(t)
	te.cfg.Concurrency = 1
	defer te.startDaemon("write")()
	ctx := context.Background()
	a, _ := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "一", Dispatch: true}))
	b, _ := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "二", Dispatch: true}))
	te.waitFor("both tasks asking the user at once", func() bool {
		as, _ := te.log.Store.Approvals(ctx, "pending", "")
		seen := map[string]bool{}
		for _, x := range as {
			if x.Review == "user" {
				seen[x.TaskID] = true
			}
		}
		return seen[a.ID] && seen[b.ID]
	})
	te.approveNext(a.ID)
	te.approveNext(b.ID)
	for _, id := range []string{a.ID, b.ID} {
		if s := te.waitStatus(id, event.TaskDone, event.TaskBlocked, event.TaskFailed); s.Status != event.TaskDone {
			t.Fatalf("task %s: %+v", id, s)
		}
	}
}

// A deploy is done only when the project's health check passes on the
// machine; a failing check blocks it and the commit is not recorded as
// deployed (4.1, 4.5).
func TestDeployWaitsForTheHealthCheck(t *testing.T) {
	te := newTaskEnv(t)
	ctx := context.Background()
	// This machine is the server the project deploys to.
	te.cfg.Role = proto.RoleServer
	te.log.Append(ctx, event.New(event.NodeRole, event.ActorSystem, "", event.NodeRolePayload{NodeID: te.cfg.NodeID, Role: proto.RoleServer}))
	defer te.startDaemon("write")()
	head := strings.TrimSpace(gitOut(t, te.repo, "rev-parse", "HEAD"))

	deploy := func(health string) store.Task {
		t.Helper()
		dep := &event.Deploy{NodeID: te.cfg.NodeID, Path: te.repo, Command: "./deploy.sh", Health: health}
		if _, err := te.tasks.PutProject(ctx, task.ProjectInput{ID: "p1", Deploy: &dep}, event.ActorUser); err != nil {
			t.Fatal(err)
		}
		tk, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Kind: event.TaskKindOps, Place: event.PlaceInPlace,
			Review: event.ReviewNone, Title: "部署 " + health, Dispatch: true, Actor: event.ActorSupervisor, DeployCommit: head})
		if err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		defer close(stop)
		go func() { // approve whatever the deploy asks: the agent's command, then the check
			for {
				select {
				case <-stop:
					return
				default:
				}
				as, _ := te.log.Store.Approvals(ctx, "pending", "")
				for _, x := range as {
					if x.TaskID == tk.ID && x.Review == "user" {
						te.hub.Decide(ctx, x.ID, x.Options[0].OptionID, "")
					}
				}
			}
		}()
		return te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	}

	bad := deploy("exit 3")
	if bad.Status != event.TaskBlocked || !strings.Contains(bad.StatusReason, "健康检查没有通过") {
		t.Fatalf("failing check: %+v", bad)
	}
	if p, _ := te.log.Store.Project(ctx, "p1"); p.DeployedCommit == head {
		t.Fatal("a failed deploy was recorded as deployed")
	}

	good := deploy("echo healthy")
	if good.Status != event.TaskDone || !strings.Contains(good.StatusReason, "健康检查通过") {
		t.Fatalf("passing check: %+v", good)
	}
	te.waitFor("the deployed commit", func() bool {
		p, _ := te.log.Store.Project(ctx, "p1")
		return p.DeployedCommit == head
	})
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

// Two in-place tasks in the same directory never run at once: the second
// waits for the first (review H2).
func TestInPlaceTasksTakeTurnsInADirectory(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	in := func(title string) store.Task {
		tk, err := te.tasks.Create(ctx, task.Input{Mode: "default", ThreadID: te.main, ProjectID: "p1", Title: title, Place: event.PlaceInPlace,
			Review: event.ReviewNone, Dispatch: true, Actor: event.ActorSupervisor})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	a := in("一")
	te.waitFor("the first task asking", func() bool {
		as, _ := te.log.Store.Approvals(ctx, "pending", "")
		return len(as) == 1 && as[0].TaskID == a.ID
	})
	b := in("二")
	te.waitFor("the second task waiting for the directory", func() bool {
		x := te.task(b.ID)
		return x.Status == event.TaskQueued && strings.Contains(x.StatusReason, "同一目录")
	})
	if runs := te.runs(b.ID); len(runs) != 0 {
		t.Fatalf("the second task started: %+v", runs)
	}
	te.approveNext(a.ID)
	te.waitStatus(a.ID, event.TaskDone)
	te.approveNext(b.ID)
	if s := te.waitStatus(b.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked); s.Status != event.TaskDone {
		t.Fatalf("second task: %+v", s)
	}
}

// Two git operations on one task at once are refused (review M4).
func TestGitOperationsOnATaskTakeTurns(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "x", Dispatch: true}))
	te.waitStatus(tk.ID, event.TaskDone)
	first, err := te.tasks.StartGit(ctx, task.GitInput{Op: "merge", TaskID: tk.ID, Into: "main", ThreadID: te.main})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := te.tasks.StartGit(ctx, task.GitInput{Op: "commit", TaskID: tk.ID, ThreadID: te.main}); err == nil || !strings.Contains(err.Error(), "还没结束") {
		t.Fatalf("a second git operation started: %v", err)
	}
	te.hub.Cancel(ctx, first.ID, "test")
}

package node_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
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

// A normal no_action turn preserves execution success and asks for acceptance.
func TestNoActionTurnWaitsForAcceptance(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("noop")()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.general(task.Input{ThreadID: te.main, Title: "部署", Dispatch: true, Review: event.ReviewNone}))
	if err != nil {
		t.Fatal(err)
	}
	waiting := te.waitStatus(tk.ID, event.TaskReview, event.TaskBlocked, event.TaskDone, event.TaskFailed)
	if waiting.Status != event.TaskReview || !task.UserReviewing(waiting) || !strings.Contains(waiting.StatusReason, "本轮正常结束") {
		t.Fatalf("task: %+v", waiting)
	}
	if n := len(te.reviewer.requests()); n != 0 {
		t.Fatalf("unexpected automatic review: %d", n)
	}
	if runs := te.runs(tk.ID); len(runs) != 1 || runs[0].Outcome != proto.OutcomeNoAction || runs[0].Status != event.RunStatusSucceeded {
		t.Fatalf("runs: %+v", runs)
	}
	if done, err := te.tasks.Verdict(ctx, tk.ID, true, "无需文件修改，回答已满足要求", event.ActorUser); err != nil || done.Status != event.TaskDone {
		t.Fatalf("acceptance: %+v %v", done, err)
	}
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

// A refusal without evidence requires acceptance, even without configured
// review; a user-requested rework retains the previous refusal.
func TestRefusalWithoutEvidenceWaitsForAcceptance(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "写 hello", Dispatch: true, Review: event.ReviewNone}))
	first := te.rejectNext(tk.ID)
	blocked := te.waitStatus(tk.ID, event.TaskReview, event.TaskBlocked, event.TaskDone)
	if blocked.Status != event.TaskReview || !strings.Contains(blocked.StatusReason, "产出尚待确认") {
		t.Fatalf("task: %+v", blocked)
	}
	if n := len(te.reviewer.requests()); n != 0 {
		t.Fatalf("reviewed a refused run (%d calls)", n)
	}

	// The user asks for rework: the same request retains the earlier refusal.
	if _, err := te.tasks.Verdict(ctx, tk.ID, false, "换个办法完成", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	te.waitFor("the second run to end", func() bool {
		return len(te.runs(tk.ID)) == 2 && te.runs(tk.ID)[1].Status != event.RunStatusRunning && te.runs(tk.ID)[1].Status != event.RunStatusQueued
	})
	te.waitStatus(tk.ID, event.TaskReview, event.TaskBlocked, event.TaskDone)
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

// Real ACP/MCP -> Node -> Hub -> Task coverage: completed work survives a
// broken connection, is not rerun, and only a separate verdict marks it done.
func TestConnectionErrorWithEvidenceWaitsForAcceptance(t *testing.T) {
	for _, mode := range []string{"result-error", "progress-error", "progress-text-error", "change-disconnect"} {
		t.Run(mode, func(t *testing.T) {
			te := newTaskEnv(t)
			defer te.startOutcomeDaemon(mode)()
			ctx := context.Background()
			tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "完成工作后断流", Review: event.ReviewNone, Dispatch: true}))
			if err != nil {
				t.Fatal(err)
			}
			waiting := te.waitStatus(tk.ID, event.TaskReview, event.TaskFailed, event.TaskBlocked, event.TaskDone)
			if waiting.Status != event.TaskReview || !strings.Contains(waiting.StatusReason, "连接中断，工作可能已完成") {
				t.Fatalf("task: %+v", waiting)
			}
			runs := te.runs(tk.ID)
			if len(runs) != 1 || runs[0].Status != event.RunStatusInterrupted || runs[0].Outcome != proto.OutcomeNeedsConfirmation {
				t.Fatalf("runs: %+v", runs)
			}
			// Effect retries must not restart work or append duplicate reports.
			for i := 0; i < 2; i++ {
				if err := te.tasks.Advance(ctx, store.Effect{Payload: []byte(runs[0].ID)}); err != nil {
					t.Fatal(err)
				}
			}
			if len(te.runs(tk.ID)) != 1 || len(te.reviewer.requests()) != 0 {
				t.Fatal("uncertain work was automatically repeated/reviewed")
			}
			if done, err := te.tasks.Verdict(ctx, tk.ID, true, "已确认工作完成", event.ActorUser); err != nil || done.Status != event.TaskDone {
				t.Fatalf("verdict: %+v %v", done, err)
			}
			if run := te.runs(tk.ID)[0]; run.Status != event.RunStatusInterrupted {
				t.Fatal("acceptance rewrote execution history")
			}
		})
	}
}

func TestCompletedDeploySurvivesRejectedFinalCheck(t *testing.T) {
	te := newTaskEnv(t)
	te.cfg.Role = proto.RoleServer
	// Fixture-only floor rule reproduces a server rejecting this final check;
	// production approval policy is unchanged.
	te.cfg.Policy.ExtraFloor = []string{`(?m)^wok check$`}
	ctx := context.Background()
	if _, err := te.log.Append(ctx, event.New(event.NodeRole, event.ActorSystem, "", event.NodeRolePayload{NodeID: te.cfg.NodeID, Role: proto.RoleServer})); err != nil {
		t.Fatal(err)
	}
	defer te.startOutcomeDaemon("result-refused")()
	tk, err := te.tasks.Create(ctx, task.Input{Mode: "default", ThreadID: te.main, ProjectID: "p1", Kind: event.TaskKindOps,
		Place: event.PlaceInPlace, Review: event.ReviewNone, Title: "部署完成后检查被拒", Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed, event.TaskReview)
	if done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	runs := te.runs(tk.ID)
	if len(runs) != 1 || runs[0].Status != event.RunStatusSucceeded || runs[0].DiffStat != "" || len(runs[0].Refused) == 0 || runs[0].Outcome != proto.OutcomeWorkProduced {
		t.Fatalf("completed deploy mistaken for no output: %+v", runs)
	}
}

func TestExplicitResultFailureStillFailsTheTask(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startOutcomeDaemon("result-failed")()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "失败声明", Review: event.ReviewNone, Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := te.waitStatus(tk.ID, event.TaskFailed, event.TaskDone, event.TaskReview); got.Status != event.TaskFailed {
		t.Fatalf("task: %+v", got)
	}
	if runs := te.runs(tk.ID); len(runs) != 1 || runs[0].Status != event.RunStatusFailed || !strings.Contains(runs[0].Error, "agent 报告任务失败") {
		t.Fatalf("runs: %+v", runs)
	}
}

// MCP report fixtures need the same local IPC endpoint as the real daemon.
func (te *taskEnv) startOutcomeDaemon(mode string) func() {
	te.t.Helper()
	d, stop := te.startDaemonWith(mode, acptest.EnvSousMCP+"=1")
	ctx, cancel := context.WithCancel(context.Background())
	if err := d.ServeIPC(ctx, node.NewLogRing(slog.NewTextHandler(io.Discard, nil))); err != nil {
		cancel()
		stop()
		te.t.Fatal(err)
	}
	return func() { cancel(); stop() }
}

func TestRefusalCancellationWithoutOutputStillFails(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("reject-stop")()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "无产出且停下", Dispatch: true, Review: event.ReviewNone}))
	if err != nil {
		t.Fatal(err)
	}
	te.rejectNext(tk.ID)
	if got := te.waitStatus(tk.ID, event.TaskFailed, event.TaskDone, event.TaskReview); got.Status != event.TaskFailed {
		t.Fatalf("task: %+v", got)
	}
	if runs := te.runs(tk.ID); len(runs) != 1 || runs[0].Status != event.RunStatusFailed || len(runs[0].Refused) == 0 || runs[0].DiffStat != "" {
		t.Fatalf("runs: %+v", runs)
	}
}

func TestReportedWorkIsNotRepeatedAfterRefusalCancellation(t *testing.T) {
	te := newTaskEnv(t)
	te.cfg.Policy.ExtraFloor = []string{`(?m)^wok check$`}
	defer te.startOutcomeDaemon("result-refused-cancel")()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "交回后拒绝导致 cancelled", Dispatch: true, Review: event.ReviewNone}))
	if err != nil {
		t.Fatal(err)
	}
	if got := te.waitStatus(tk.ID, event.TaskReview, event.TaskFailed, event.TaskDone); got.Status != event.TaskReview {
		t.Fatalf("task: %+v", got)
	}
	run := te.runs(tk.ID)[0]
	if run.Status != event.RunStatusInterrupted || len(run.Refused) != 1 {
		t.Fatalf("result was retried or failed: %+v", run)
	}
}

func TestPersistedResultSurvivesDaemonInterruption(t *testing.T) {
	te := newTaskEnv(t)
	stop := te.startOutcomeDaemon("result-slow")
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "交回后 daemon 重启", Dispatch: true, Review: event.ReviewNone}))
	if err != nil {
		t.Fatal(err)
	}
	run := te.firstRun(tk.ID)
	te.waitFor("persisted result", func() bool {
		raw, _ := os.ReadFile(filepath.Join(te.root, "runs", run.ID+".json"))
		return strings.Contains(string(raw), `"result":`) && strings.Contains(string(raw), `"work_baseline":`)
	})
	stop()
	stop = nil
	defer te.startOutcomeDaemon("noop")()
	if got := te.waitStatus(tk.ID, event.TaskReview, event.TaskFailed, event.TaskDone); got.Status != event.TaskReview {
		t.Fatalf("task: %+v", got)
	}
	if runs := te.runs(tk.ID); len(runs) != 1 || runs[0].Status != event.RunStatusInterrupted || runs[0].Outcome != proto.OutcomeNeedsConfirmation || !strings.Contains(runs[0].Summary, "部署执行结果") {
		t.Fatalf("runs: %+v", runs)
	}
}

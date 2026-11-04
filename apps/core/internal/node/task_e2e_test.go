package node_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/effects"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// verdicts answers reviewer and approver calls with canned JSON, in order,
// repeating the last one.
type verdicts struct {
	mu      sync.Mutex
	answers []string
	calls   []provider.Request
}

func (v *verdicts) Name() string { return "canned" }
func (v *verdicts) Stream(_ context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i := min(len(v.calls), len(v.answers)-1)
	v.calls = append(v.calls, req)
	return provider.Response{Message: provider.Text(provider.RoleAssistant, v.answers[i])}, nil
}

func (v *verdicts) requests() []provider.Request {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]provider.Request{}, v.calls...)
}

type taskEnv struct {
	*env
	tasks    *task.Engine
	reviewer *verdicts
	approver *verdicts
	project  store.Project
	repo     string
	origin   string
	ghLog    string
}

// newTaskEnv adds the task engine and the effects runner to a Hub, and a
// project whose repository has a bare origin and a fake gh on PATH.
func newTaskEnv(t *testing.T, review ...string) *taskEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	e := newEnv(t)
	te := &taskEnv{env: e, reviewer: &verdicts{answers: review}, approver: &verdicts{answers: []string{`{"decision":"allow","reason":"构建需要"}`}}}
	if len(review) == 0 {
		te.reviewer.answers = []string{`{"pass":true,"reason":"新增了 hello.txt"}`}
	}
	te.tasks = task.New(e.log, e.hub)
	te.tasks.Reviewer = task.Model{Provider: te.reviewer, Name: "r"}
	te.tasks.Approver = task.Model{Provider: te.approver, Name: "a"}
	ctx, cancel := context.WithCancel(context.Background())
	runner := effects.NewRunner(e.log.Store, e.log.EffectsEnqueued(), 50*time.Millisecond)
	runner.Handle(store.EffectTaskAdvance, te.tasks.Advance)
	runner.Handle(store.EffectApprove, te.tasks.Approve)
	runner.Handle(store.EffectSetup, te.tasks.Setup)
	runner.Handle(store.EffectPush, func(context.Context, store.Effect) error { return nil })
	te.tasks.Start(ctx)
	go runner.Run(ctx)
	t.Cleanup(cancel)

	te.repo = gitRepo(t)
	te.origin = filepath.Join(t.TempDir(), "origin.git")
	exec.Command("git", "init", "-q", "--bare", te.origin).Run()
	exec.Command("git", "-C", te.repo, "remote", "add", "origin", te.origin).Run()
	bin := t.TempDir()
	te.ghLog = filepath.Join(bin, "gh.log")
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho \"$@\" >> "+te.ghLog+"\n"+
		"case \"$2\" in view) [ -f "+te.ghLog+".made ] || exit 1; echo '{\"url\":\"https://github.com/u/demo/pull/1\",\"state\":\"OPEN\",\"mergeable\":\"MERGEABLE\"}';;\n"+
		"create) touch "+te.ghLog+".made; echo https://github.com/u/demo/pull/1;; esac\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ev := event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{
		ProjectID: "p1", Name: "demo", DefaultAgent: "fake", Nodes: []event.ProjectNode{{NodeID: e.cfg.NodeID, Path: te.repo}},
	})
	if _, err := e.log.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	te.project, _ = e.log.Store.Project(context.Background(), "p1")
	return te
}

func (te *taskEnv) task(id string) store.Task {
	t, err := te.log.Store.Task(context.Background(), id)
	if err != nil {
		te.t.Fatal(err)
	}
	return t
}

func (te *taskEnv) waitStatus(id string, want ...string) store.Task {
	te.t.Helper()
	var t store.Task
	te.waitFor("task status "+strings.Join(want, "/"), func() bool {
		t = te.task(id)
		for _, w := range want {
			if t.Status == w {
				return true
			}
		}
		return false
	})
	return t
}

func (te *taskEnv) runs(taskID string) []store.Run {
	rs, _ := te.log.Store.Runs(context.Background(), store.RunFilter{TaskID: taskID})
	return rs
}

// approveNext allows the next pending approval of the task.
func (te *taskEnv) approveNext(taskID string) store.Approval {
	te.t.Helper()
	var a store.Approval
	te.waitFor("an approval for the task", func() bool {
		as, _ := te.log.Store.Approvals(context.Background(), "pending", "")
		for _, x := range as {
			if x.TaskID == taskID && x.Review == "user" {
				a = x
				return true
			}
		}
		return false
	})
	if err := te.hub.Decide(context.Background(), a.ID, a.Options[0].OptionID, ""); err != nil {
		te.t.Fatal(err)
	}
	return a
}

func TestTaskCoreLoop(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()

	tk, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "Add hello", Detail: "创建 hello.txt", Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Kind != event.TaskKindCode {
		t.Fatalf("kind: %s", tk.Kind)
	}
	// The agent asks to run a shell command: not on the safe list, so the user decides.
	a := te.approveNext(tk.ID)
	if a.Category != "exec-other" || te.task(tk.ID).Status != event.TaskRunning && te.task(tk.ID).Status != event.TaskWaitingApproval {
		t.Fatalf("approval %+v, task %s", a, te.task(tk.ID).Status)
	}
	// Then the reviewer passes it, and delivery asks to push.
	push := te.approveNext(tk.ID)
	if push.Category != "git-remote" || !strings.Contains(push.Title, "push") || !strings.Contains(push.Detail, "提交") {
		t.Fatalf("push approval: %+v", push)
	}
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	if done.Status != event.TaskDone || done.PRURL != "https://github.com/u/demo/pull/1" || !strings.HasPrefix(done.Branch, "sous/add-hello-") {
		t.Fatalf("task: %+v", done)
	}

	runs := te.runs(tk.ID)
	if len(runs) != 2 || runs[0].Purpose != "work" || runs[1].Purpose != "deliver" || runs[1].Agent != "sous" {
		t.Fatalf("runs: %+v", runs)
	}
	if out, _ := exec.Command("git", "-C", te.origin, "log", "--oneline", done.Branch).Output(); !strings.Contains(string(out), "Add hello") {
		t.Fatalf("origin has the branch with a commit: %s", out)
	}
	gh, _ := os.ReadFile(te.ghLog)
	if !strings.Contains(string(gh), "pr create --head "+done.Branch) {
		t.Fatalf("gh: %s", gh)
	}
	// The reviewer saw the full diff.
	reqs := te.reviewer.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Messages[0].PlainText(), "+hi") || !strings.Contains(reqs[0].Messages[0].PlainText(), "hello.txt") {
		t.Fatalf("review request: %+v", reqs)
	}
	// A report lands in the thread, with the PR link.
	te.waitFor("the report", func() bool {
		msgs, _ := te.log.Store.Messages(ctx, te.main, 0, 50)
		for _, m := range msgs {
			if m.Kind == event.KindReport && strings.Contains(m.Content, "pull/1") && strings.Contains(m.Content, "完成") {
				return true
			}
		}
		return false
	})
	// The closed task's worktree goes away; its branch stays.
	te.waitFor("the worktree to be removed", func() bool {
		_, err := os.Stat(filepath.Join(te.root, "worktrees", "demo"))
		entries, _ := os.ReadDir(filepath.Join(te.root, "worktrees", "demo"))
		return err == nil && len(entries) == 0
	})
	if out, _ := exec.Command("git", "-C", te.repo, "branch").Output(); !strings.Contains(string(out), done.Branch) {
		t.Fatal("the branch was deleted with the worktree")
	}
	// The user's checkout is untouched.
	if _, err := os.Stat(filepath.Join(te.repo, "hello.txt")); err == nil {
		t.Fatal("the user's checkout was modified")
	}
}

func TestReviewSendsBackThenPasses(t *testing.T) {
	te := newTaskEnv(t, `{"pass":false,"reason":"还要加一个 README 说明"}`, `{"pass":true,"reason":"好了"}`)
	defer te.startDaemon("edit")() // worktree edits: no approvals needed in default mode
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "Add hello", Dispatch: true, Actor: event.ActorSupervisor})

	te.waitFor("a second work run", func() bool { return len(te.runs(tk.ID)) >= 2 })
	second := te.runs(tk.ID)[1]
	if second.Purpose != "work" || !strings.Contains(second.Prompt, "还要加一个 README 说明") || !strings.Contains(second.Prompt, "第 2 次") {
		t.Fatalf("rework run: %+v", second)
	}
	te.approveNext(tk.ID) // the push
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	// Both work runs used the same worktree and branch.
	runs := te.runs(tk.ID)
	if runs[0].Workdir != runs[1].Workdir || runs[0].Branch != runs[1].Branch {
		t.Fatalf("runs did not share the worktree: %+v", runs)
	}
	evs, _ := te.log.Store.EventsSince(ctx, 0, 1000)
	reviews := 0
	for _, ev := range evs {
		if ev.Type == event.TaskReviewed && ev.TaskID == tk.ID {
			reviews++
		}
	}
	if reviews != 2 {
		t.Fatalf("reviews recorded: %d", reviews)
	}
}

func TestReviewGivesUpAfterThreeTries(t *testing.T) {
	te := newTaskEnv(t, `{"pass":false,"reason":"不对"}`)
	defer te.startDaemon("edit")()
	tk, _ := te.tasks.Create(context.Background(), task.Input{ProjectID: "p1", Title: "x", Dispatch: true, Actor: event.ActorSupervisor})
	blocked := te.waitStatus(tk.ID, event.TaskBlocked, event.TaskDone)
	if blocked.Status != event.TaskBlocked || !strings.Contains(blocked.StatusReason, "3 次") || len(te.runs(tk.ID)) != task.MaxWorkRuns {
		t.Fatalf("task %+v after %d runs", blocked, len(te.runs(tk.ID)))
	}
}

func TestAutoModeApprover(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "Add hello", Mode: "auto", Dispatch: true, Actor: event.ActorSupervisor})
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	// The approver decided both the command and the push; the user was never asked.
	as, _ := te.log.Store.Approvals(ctx, "", "")
	if len(as) != 2 {
		t.Fatalf("approvals: %+v", as)
	}
	for _, a := range as {
		if a.Review != "ai" || a.DecidedBy != event.ActorApprover || a.Status != "decided" {
			t.Fatalf("approval: %+v", a)
		}
	}
	if reqs := te.approver.requests(); len(reqs) != 2 || !strings.Contains(reqs[0].System, "审批员") {
		t.Fatalf("approver calls: %d", len(reqs))
	}
}

func TestApproverEscalatesToUser(t *testing.T) {
	te := newTaskEnv(t)
	te.approver.answers = []string{`{"decision":"ask_user","reason":"和任务无关"}`}
	defer te.startDaemon("write")()
	tk, _ := te.tasks.Create(context.Background(), task.Input{ProjectID: "p1", Title: "x", Mode: "auto", Dispatch: true, Actor: event.ActorSupervisor})
	a := te.approveNext(tk.ID)
	if a.Escalation != "和任务无关" {
		t.Fatalf("escalated approval: %+v", a)
	}
}

func TestFloorRuleRefusesInOpenMode(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("floor")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "x", Mode: "open", Dispatch: true, Actor: event.ActorSupervisor})
	te.waitFor("the work run to end", func() bool {
		rs := te.runs(tk.ID)
		return len(rs) > 0 && rs[0].Status != event.RunStatusQueued && rs[0].Status != event.RunStatusRunning
	})
	r := te.runs(tk.ID)[0]
	if !strings.Contains(r.Summary, "没有获得写入权限") {
		t.Fatalf("the floor did not refuse: %+v", r)
	}
	evs, _ := te.log.Store.RunEvents(ctx, r.ID)
	found := false
	for _, ev := range evs {
		var p event.RunUpdatePayload
		ev.Decode(&p)
		found = found || ev.Type == event.RunUpdate && p.Kind == "permission" && p.Decision == "deny" && p.Category == "floor"
	}
	if !found {
		t.Fatal("the refusal is not in the event log")
	}
}

func TestTaskQueuesUntilMachineComesOnline(t *testing.T) {
	te := newTaskEnv(t)
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "x", Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	q := te.task(tk.ID)
	if q.Status != event.TaskQueued || !strings.Contains(q.StatusReason, "离线") {
		t.Fatalf("task: %+v", q)
	}
	// The user hears about it once.
	evs, _ := te.log.Store.EventsSince(ctx, 0, 1000)
	pushes := 0
	for _, ev := range evs {
		if event.PushKind(ev) != "" && ev.TaskID == tk.ID {
			pushes++
		}
	}
	if pushes != 1 {
		t.Fatalf("push notifications for the queued task: %d", pushes)
	}
	defer te.startDaemon("edit")()
	te.approveNext(tk.ID) // the push, once the work passed review
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
}

func TestDependentTaskWaits(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	first, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "first", Actor: event.ActorSupervisor})
	second, err := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "second", DependsOn: []string{first.ID}, Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	if s := te.task(second.ID); s.Status != event.TaskQueued || !strings.Contains(s.StatusReason, "first") {
		t.Fatalf("second: %+v", s)
	}
	if _, err := te.tasks.Update(ctx, first.ID, event.TaskFields{DependsOn: []string{second.ID}, SetDeps: true}, event.ActorUser); err == nil {
		t.Fatal("a dependency cycle was accepted")
	}
	te.tasks.Dispatch(ctx, first.ID, "", "", event.ActorUser)
	te.approveNext(first.ID)
	te.waitStatus(first.ID, event.TaskDone)
	// Once the first is done the second starts by itself.
	te.waitFor("the second task to run", func() bool { return len(te.runs(second.ID)) > 0 })
}

func TestCancelTaskStopsRunAndSubtasks(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("slow")()
	ctx := context.Background()
	parent, _ := te.tasks.Create(ctx, task.Input{Title: "清理磁盘", Dispatch: true, Actor: event.ActorSupervisor}) // a general task
	child, _ := te.tasks.Create(ctx, task.Input{Title: "子任务", ParentID: parent.ID, Actor: event.ActorSupervisor})
	te.waitFor("the run to start", func() bool {
		rs := te.runs(parent.ID)
		return len(rs) == 1 && rs[0].Status == event.RunStatusRunning
	})
	if err := te.tasks.Cancel(ctx, parent.ID, "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	if te.task(child.ID).Status != event.TaskCancelled {
		t.Fatal("the subtask is still open")
	}
	te.waitFor("the run to stop", func() bool { return te.runs(parent.ID)[0].Status == event.RunStatusCancelled })
	time.Sleep(200 * time.Millisecond) // the run's end must not reopen the task
	if te.task(parent.ID).Status != event.TaskCancelled {
		t.Fatalf("task: %+v", te.task(parent.ID))
	}
}

func TestGeneralTaskDoneWithoutDelivery(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	tk, _ := te.tasks.Create(context.Background(), task.Input{Title: "查一下磁盘占用", Dispatch: true, Actor: event.ActorSupervisor})
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	if done.Status != event.TaskDone || done.PRURL != "" || len(te.runs(tk.ID)) != 1 {
		t.Fatalf("task %+v runs %+v", done, te.runs(tk.ID))
	}
}

func TestRetryGetsFreshReviewRounds(t *testing.T) {
	te := newTaskEnv(t, `{"pass":false,"reason":"不对"}`)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "x", Dispatch: true, Actor: event.ActorSupervisor})
	te.waitStatus(tk.ID, event.TaskBlocked)
	time.Sleep(5 * time.Millisecond) // the retry is later than the last run
	if _, err := te.tasks.Dispatch(ctx, tk.ID, "", "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	te.waitFor("three more work runs", func() bool { return len(te.runs(tk.ID)) == 2*task.MaxWorkRuns })
	if b := te.waitStatus(tk.ID, event.TaskBlocked); !strings.Contains(b.StatusReason, "3 次") {
		t.Fatalf("task: %+v", b)
	}
}

func TestEditingDetailReachesTheRunningAgent(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "x", Detail: "旧说明", Dispatch: true, Actor: event.ActorSupervisor})
	var a store.Approval
	te.waitFor("the agent to ask", func() bool {
		as, _ := te.log.Store.Approvals(ctx, "pending", "")
		if len(as) > 0 {
			a = as[0]
		}
		return len(as) > 0
	})
	detail := "新说明：README 用英文"
	if _, err := te.tasks.Update(ctx, tk.ID, event.TaskFields{Detail: &detail}, event.ActorSupervisor); err != nil {
		t.Fatal(err)
	}
	te.waitFor("the daemon to queue the follow-up", func() bool {
		log, _ := os.ReadFile(te.hub.LogPath(a.RunID))
		return strings.Contains(string(log), "收到追加指令")
	})
	te.hub.Decide(ctx, a.ID, "allow", "")
}

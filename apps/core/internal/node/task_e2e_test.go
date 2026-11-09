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
	te := &taskEnv{env: e, reviewer: &verdicts{answers: review}, approver: &verdicts{answers: []string{`{"decision":"allow","confidence":"high","reason":"构建需要"}`}}}
	if len(review) == 0 {
		te.reviewer.answers = []string{`{"pass":true,"reason":"新增了 hello.txt"}`}
	}
	te.tasks = e.engine
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

// code is a task in its own worktree, as the supervisor would ask for one:
// starting from the checkout's current branch (main), locally, reviewed by
// the reviewer model and reworked when it fails. Nothing here is a default
// of sous; the tests spell it out (decision 0022).
func (te *taskEnv) code(in task.Input) task.Input {
	if in.ProjectID == "" {
		in.ProjectID = "p1"
	}
	if in.Actor == "" {
		in.Actor = event.ActorSupervisor
	}
	in.Place = event.PlaceWorktree
	if in.Review == "" {
		in.Review = event.ReviewModel
	}
	if in.OnFail == "" {
		in.OnFail = event.OnFailRework
	}
	if in.BaseBranch == "" {
		in.BaseBranch, in.BaseFrom = "main", event.BaseLocal
	}
	return in
}

// general is a task in a scratch directory, reviewed by the reviewer model.
func (te *taskEnv) general(in task.Input) task.Input {
	if in.Actor == "" {
		in.Actor = event.ActorSupervisor
	}
	in.Place = event.PlaceScratch
	if in.Review == "" {
		in.Review = event.ReviewModel
	}
	if in.OnFail == "" {
		in.OnFail = event.OnFailRework
	}
	return in
}

// gitOp runs a git operation in the background (it may wait for an
// approval) and returns its result channel.
func (te *taskEnv) gitOp(in task.GitInput) <-chan store.Run {
	ch := make(chan store.Run, 1)
	go func() {
		r, done, err := te.tasks.Git(context.Background(), in, 20*time.Second)
		if err != nil || !done {
			te.t.Errorf("git %s: done=%v err=%v", in.Op, done, err)
		}
		ch <- r
	}()
	return ch
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

// The work ends with the changes on the task's branch: nothing is
// committed, merged, pushed or opened as a pull request until the user asks
// (decision 0022). Then merge, push and undo are separate operations.
func TestTaskCoreLoop(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()

	tk, err := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "Add hello", Detail: "创建 hello.txt", Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	if tk.Kind != event.TaskKindCode || tk.Place != event.PlaceWorktree || tk.BaseBranch != "main" || tk.BaseFrom != "local" {
		t.Fatalf("task: %+v", tk)
	}
	// The agent asks to run a shell command: not on the safe list, so the user decides.
	a := te.approveNext(tk.ID)
	if a.Category != "exec-other" {
		t.Fatalf("approval %+v", a)
	}
	// The reviewer passes it, and the task is done: no push was asked for.
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	// TaskDone and the following GitState projection are separate events.
	// Wait for the latter before asserting both, also under the race detector.
	if done.Status == event.TaskDone {
		te.waitFor("the unmerged state", func() bool { done = te.task(tk.ID); return done.GitState == event.GitUnmerged })
	}
	if done.Status != event.TaskDone || done.PRURL != "" || !strings.HasPrefix(done.Branch, "sous/add-hello-") ||
		done.GitState != event.GitUnmerged || !strings.Contains(done.StatusReason, "还没合并") || done.BaseCommit == "" {
		t.Fatalf("task: %+v", done)
	}
	runs := te.runs(tk.ID)
	if len(runs) != 1 || runs[0].Purpose != "work" {
		t.Fatalf("runs: %+v", runs)
	}
	if gh, _ := os.ReadFile(te.ghLog); len(gh) > 0 {
		t.Fatalf("gh was called: %s", gh)
	}
	if out, _ := exec.Command("git", "-C", te.origin, "branch").Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("something was pushed: %s", out)
	}
	// The reviewer saw the full diff.
	reqs := te.reviewer.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Messages[0].PlainText(), "+hi") || !strings.Contains(reqs[0].Messages[0].PlainText(), "hello.txt") {
		t.Fatalf("review request: %+v", reqs)
	}
	// The report says where the changes are.
	te.waitFor("the report", func() bool {
		msgs, _ := te.log.Store.Messages(ctx, te.main, 0, 50)
		for _, m := range msgs {
			if m.Kind == event.KindReport && strings.Contains(m.Content, done.Branch) && strings.Contains(m.Content, "没有合并也没有推送") {
				return true
			}
		}
		return false
	})
	// The worktree stays while the branch is not merged, and the user's
	// checkout is untouched.
	wt := runs[0].Workdir
	if _, err := os.Stat(filepath.Join(wt, "hello.txt")); err != nil {
		t.Fatal("the task's worktree is gone before the branch was merged")
	}
	if _, err := os.Stat(filepath.Join(te.repo, "hello.txt")); err == nil {
		t.Fatal("the user's checkout was modified")
	}

	// "合并到当前分支": main is checked out in the user's checkout, so the
	// merge touches it and the user approves.
	merged := te.gitOp(task.GitInput{Op: "merge", TaskID: tk.ID, Into: "main", ThreadID: te.main})
	if ma := te.approveNext(tk.ID); ma.Category != "outside-worktree" || !strings.Contains(ma.Detail, "main") {
		t.Fatalf("merge approval: %+v", ma)
	}
	mr := <-merged
	if mr.Status != event.RunStatusSucceeded || mr.GitResult == nil || mr.GitResult.Commits != 1 || !strings.Contains(task.GitReport(mr), "没有推送") {
		t.Fatalf("merge run: %+v %+v", mr, mr.GitResult)
	}
	if _, err := os.Stat(filepath.Join(te.repo, "hello.txt")); err != nil {
		t.Fatal("main in the user's checkout did not get the change")
	}
	te.waitFor("the merged state", func() bool { return te.task(tk.ID).GitState == event.GitMerged })
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("the worktree is still there after the merge")
	}

	// "推送": main is a protected branch, so the user approves; nothing is forced.
	pushed := te.gitOp(task.GitInput{Op: "push", ProjectID: "p1", Branch: "main", ThreadID: te.main})
	te.waitFor("the push approval", func() bool {
		as, _ := te.log.Store.Approvals(ctx, "pending", "")
		for _, x := range as {
			if x.Category == "git-remote" && strings.Contains(x.Detail, "git push origin main") {
				te.hub.Decide(ctx, x.ID, "allow", "")
				return true
			}
		}
		return false
	})
	if pr := <-pushed; pr.Status != event.RunStatusSucceeded || !strings.Contains(task.GitReport(pr), "已把 main 推送到 origin") {
		t.Fatalf("push run: %+v", pr)
	}
	if out, _ := exec.Command("git", "-C", te.origin, "log", "--format=%s", "main").Output(); !strings.Contains(string(out), "Add hello") {
		t.Fatalf("origin main: %s", out)
	}
	// What is pushed cannot be undone.
	undone := te.gitOp(task.GitInput{Op: "undo", ProjectID: "p1", Branch: "main", ThreadID: te.main})
	if ur := <-undone; ur.Status != event.RunStatusFailed || !strings.Contains(task.GitReport(ur), "推送") {
		t.Fatalf("undo after push: %+v", ur)
	}
	// A merged branch is not worked on again.
	if _, err := te.tasks.Dispatch(ctx, tk.ID, "", "", event.ActorUser); err == nil {
		t.Fatal("a merged task was dispatched again")
	}
}

func TestReviewSendsBackThenPasses(t *testing.T) {
	te := newTaskEnv(t, `{"pass":false,"reason":"还要加一个 README 说明"}`, `{"pass":true,"reason":"好了"}`)
	defer te.startDaemon("edit")() // worktree edits: no approvals needed in default mode
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Title: "Add hello", Dispatch: true}))

	te.waitFor("a second work run", func() bool { return len(te.runs(tk.ID)) >= 2 })
	second := te.runs(tk.ID)[1]
	if second.Purpose != "work" || !strings.Contains(second.Prompt, "还要加一个 README 说明") || !strings.Contains(second.Prompt, "第 2 次") {
		t.Fatalf("rework run: %+v", second)
	}
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
	tk, _ := te.tasks.Create(context.Background(), te.code(task.Input{Title: "x", Dispatch: true}))
	blocked := te.waitStatus(tk.ID, event.TaskBlocked, event.TaskDone)
	if blocked.Status != event.TaskBlocked || !strings.Contains(blocked.StatusReason, "3 次") || len(te.runs(tk.ID)) != task.MaxWorkRuns {
		t.Fatalf("task %+v after %d runs", blocked, len(te.runs(tk.ID)))
	}
}

func TestAutoModeApprover(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("write")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Title: "Add hello", Mode: "auto", Dispatch: true}))
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	// The approver decided the command; the user was never asked.
	as, _ := te.log.Store.Approvals(ctx, "", "")
	if len(as) != 1 {
		t.Fatalf("approvals: %+v", as)
	}
	for _, a := range as {
		if a.Review != "ai" || a.DecidedBy != event.ActorApprover || a.Status != "decided" {
			t.Fatalf("approval: %+v", a)
		}
	}
	if reqs := te.approver.requests(); len(reqs) != 1 || !strings.Contains(reqs[0].System, "审批员") {
		t.Fatalf("approver calls: %d", len(reqs))
	}
	// Pushing the task branch (not a protected one) in the auto mode is a
	// routine action the node allows itself (docs/decisions/0019).
	if _, err := te.tasks.PutProject(ctx, task.ProjectInput{ID: "p1", DefaultMode: ptr("auto")}, event.ActorUser); err != nil {
		t.Fatal(err)
	}
	branch := te.task(tk.ID).Branch
	r := <-te.gitOp(task.GitInput{Op: "push", ProjectID: "p1", Branch: branch})
	if r.Status != event.RunStatusSucceeded {
		t.Fatalf("push: %+v", r)
	}
	evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
	pushed := false
	for _, ev := range evs {
		var p event.RunUpdatePayload
		if ev.Type == event.RunUpdate && ev.Decode(&p) == nil && p.Kind == "permission" && p.Decision == "allow" && strings.Contains(p.Title, "推送") && strings.Contains(p.Title, "常规操作") {
			pushed = true
		}
	}
	if !pushed {
		t.Error("the routine push was not recorded")
	}
}

func ptr[T any](v T) *T { return &v }

func TestApproverEscalatesToUser(t *testing.T) {
	te := newTaskEnv(t)
	te.approver.answers = []string{`{"decision":"ask_user","confidence":"high","reason":"和任务无关"}`}
	defer te.startDaemon("write")()
	tk, _ := te.tasks.Create(context.Background(), te.code(task.Input{Title: "x", Mode: "auto", Dispatch: true}))
	a := te.approveNext(tk.ID)
	if a.Escalation != "和任务无关" {
		t.Fatalf("escalated approval: %+v", a)
	}
}

// A request with a hard signal goes to the user even in the auto mode; the
// approver agent is never asked (docs/decisions/0019).
func TestHardSignalSkipsTheApprover(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("secret")()
	tk, _ := te.tasks.Create(context.Background(), te.code(task.Input{Title: "x", Mode: "auto", Dispatch: true}))
	a := te.approveNext(tk.ID)
	if a.Review != "user" || !strings.Contains(a.Detail, "高风险信号：读写凭据或密钥") {
		t.Fatalf("approval: %+v", a)
	}
	if n := len(te.approver.requests()); n != 0 {
		t.Fatalf("the approver was asked %d times", n)
	}
}

func TestFloorRuleRefusesInOpenMode(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("floor")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Title: "x", Mode: "open", Dispatch: true}))
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
	tk, err := te.tasks.Create(ctx, te.code(task.Input{Title: "x", Dispatch: true}))
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
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
}

func TestDependentTaskWaits(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	first, _ := te.tasks.Create(ctx, te.code(task.Input{Title: "first"}))
	second, err := te.tasks.Create(ctx, te.code(task.Input{Title: "second", DependsOn: []string{first.ID}, Dispatch: true}))
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
	te.waitStatus(first.ID, event.TaskDone)
	// Once the first is done the second starts by itself.
	te.waitFor("the second task to run", func() bool { return len(te.runs(second.ID)) > 0 })
}

func TestCancelTaskStopsRunAndSubtasks(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("slow")()
	ctx := context.Background()
	parent, _ := te.tasks.Create(ctx, te.general(task.Input{Title: "清理磁盘", Dispatch: true}))
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
	tk, _ := te.tasks.Create(context.Background(), te.general(task.Input{Title: "查一下磁盘占用", Dispatch: true}))
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	if done.Status != event.TaskDone || done.PRURL != "" || done.GitState != "" || len(te.runs(tk.ID)) != 1 {
		t.Fatalf("task %+v runs %+v", done, te.runs(tk.ID))
	}
}

func TestRetryGetsFreshReviewRounds(t *testing.T) {
	te := newTaskEnv(t, `{"pass":false,"reason":"不对"}`)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Title: "x", Dispatch: true}))
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
	tk, _ := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", Title: "x", Detail: "旧说明", Dispatch: true}))
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

// A task that starts from the remote (base_from remote) gets the remote's
// latest code even when this checkout is behind.
func TestTaskStartsFromRemoteLatest(t *testing.T) {
	te := newTaskEnv(t)
	ctx := context.Background()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(te.repo, "push", "-q", "origin", "HEAD:refs/heads/main")
	git(te.origin, "symbolic-ref", "HEAD", "refs/heads/main")
	// Another machine pushes a commit this checkout has not fetched.
	other := filepath.Join(t.TempDir(), "other")
	git(te.repo, "clone", "-q", te.origin, other)
	os.WriteFile(filepath.Join(other, "upstream.txt"), []byte("from the other machine\n"), 0o644)
	git(other, "add", "-A")
	git(other, "commit", "-qm", "Upstream change")
	git(other, "push", "-q", "origin", "main")
	before := git(te.repo, "rev-parse", "HEAD")
	defer te.startDaemon("write")()

	tk, err := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: "Add hello", Detail: "创建 hello.txt", Dispatch: true,
		BaseBranch: "main", BaseFrom: event.BaseRemote}))
	if err != nil {
		t.Fatal(err)
	}
	te.approveNext(tk.ID)
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	if done.Status != event.TaskDone || done.BaseBranch != "main" || done.BaseFrom != "remote" {
		t.Fatalf("task: %+v", done)
	}
	// The task branch is on top of the other machine's commit (it was
	// committed when the user asked, here), and the review saw only the
	// task's own change.
	if r := <-te.gitOp(task.GitInput{Op: "commit", TaskID: tk.ID}); r.Status != event.RunStatusSucceeded {
		t.Fatalf("commit: %+v", r)
	}
	if log := git(te.repo, "log", "--format=%s", done.Branch); !strings.Contains(log, "Upstream change") || !strings.Contains(log, "Add hello") {
		t.Fatalf("branch history: %s", log)
	}
	if reqs := te.reviewer.requests(); len(reqs) != 1 || strings.Contains(reqs[0].Messages[0].PlainText(), "upstream.txt") {
		t.Fatalf("review saw other changes: %+v", reqs)
	}
	// The run said where it started; the user's checkout did not move.
	var said bool
	evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
	for _, ev := range evs {
		if ev.Type == event.RunUpdate && strings.Contains(string(ev.Payload), "origin/main") {
			said = true
		}
	}
	if !said {
		t.Error("the run did not say it started from origin/main")
	}
	if git(te.repo, "rev-parse", "HEAD") != before {
		t.Error("the user's checkout moved")
	}
}

// Two open tasks of a project that change the same file: the supervisor
// hears about it once (multi-machine-sync, R5).
func TestOverlappingTasksAreReported(t *testing.T) {
	te := newTaskEnv(t, `{"pass":true,"reason":"ok"}`, `{"pass":true,"reason":"ok"}`)
	defer te.startDaemon("write")()
	ctx := context.Background()
	mk := func(title string) store.Task {
		tk, err := te.tasks.Create(ctx, te.code(task.Input{Mode: "default", ThreadID: te.main, Title: title, Detail: "创建 hello.txt", Dispatch: true}))
		if err != nil {
			t.Fatal(err)
		}
		te.approveNext(tk.ID) // the shell command
		return tk
	}
	a := mk("First")
	// A is done but its branch is not merged: it still counts.
	te.waitStatus(a.ID, event.TaskDone)
	b := mk("Second")
	notices := func() (n int, text string) {
		evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
		for _, ev := range evs {
			var p event.TriggerPayload
			if ev.Type == event.TriggerFired && ev.Decode(&p) == nil && strings.HasPrefix(p.Key, "overlap:") {
				n++
				text = p.Content
			}
		}
		return
	}
	te.waitFor("the overlap notice", func() bool { n, _ := notices(); return n > 0 })
	n, text := notices()
	if n != 1 || !strings.Contains(text, "hello.txt") || !strings.Contains(text, a.ID) || !strings.Contains(text, b.ID) {
		t.Fatalf("%d notices: %s", n, text)
	}
}

func TestUnsetPermissionModeDispatchesAuto(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	tk, err := te.tasks.Create(context.Background(), te.code(task.Input{Title: "默认自动权限", Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	te.waitStatus(tk.ID, event.TaskDone)
	runs := te.runs(tk.ID)
	if len(runs) != 1 || runs[0].Mode != "auto" {
		t.Fatalf("runs: %+v", runs)
	}
}

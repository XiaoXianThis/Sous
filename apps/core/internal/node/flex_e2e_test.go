package node_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// End-to-end tests of decision 0022 (work/specs/flexible-workflow): reviews
// chosen per task, in-place tasks with snapshots, git operations on
// request, and the project consultant.

// callTool calls one of the supervisor's task tools in a turn of the main
// thread.
func (te *taskEnv) callTool(name string, args any, replyTo int64) (string, error) {
	te.t.Helper()
	raw, _ := json.Marshal(args)
	for _, tl := range te.tasks.Tools() {
		if tl.Spec().Name == name {
			return tl.Call(tool.WithTurn(context.Background(), tool.Turn{ThreadID: te.main, ReplyTo: replyTo}), raw)
		}
	}
	te.t.Fatalf("no tool %s", name)
	return "", nil
}

// A task the user reviews waits in "needs you"; a failed verdict sends it
// back with the user's words, a passing one closes it.
func TestUserReviewAndVerdict(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "Add hello", Dispatch: true, Review: event.ReviewUser}))
	if err != nil {
		t.Fatal(err)
	}
	waiting := te.waitStatus(tk.ID, event.TaskReview)
	te.waitFor("the user review", func() bool { return task.UserReviewing(te.task(tk.ID)) })
	if len(te.reviewer.requests()) != 0 {
		t.Fatal("the reviewer model was asked")
	}
	var inbox struct{ Tasks []store.Task }
	resp := getJSON(t, te.env, "/api/v1/inbox", &inbox)
	if resp != 200 || len(inbox.Tasks) != 1 || inbox.Tasks[0].ID != tk.ID {
		t.Fatalf("inbox %d %+v (task %+v)", resp, inbox, waiting)
	}
	if _, err := te.tasks.Verdict(ctx, tk.ID, false, "", event.ActorUser); err == nil {
		t.Fatal("a failed verdict without a reason was accepted")
	}
	if _, err := te.tasks.Verdict(ctx, tk.ID, false, "README 也要改", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	te.waitFor("the rework run", func() bool { return len(te.runs(tk.ID)) == 2 })
	if p := te.runs(tk.ID)[1].Prompt; !strings.Contains(p, "README 也要改") {
		t.Fatalf("rework prompt: %s", p)
	}
	te.waitFor("the second review", func() bool {
		return task.UserReviewing(te.task(tk.ID)) && len(te.runs(tk.ID)) == 2 && te.runs(tk.ID)[1].Status == event.RunStatusSucceeded
	})
	if _, err := te.callTool("task_verdict", map[string]any{"task": tk.ID, "pass": true}, 0); err != nil {
		t.Fatal(err)
	}
	if done := te.waitStatus(tk.ID, event.TaskDone); done.GitState != event.GitUnmerged {
		t.Fatalf("task: %+v", done)
	}
}

// getJSON reads an API endpoint as a logged-in user (dev login).
func getJSON(t *testing.T, e *env, path string, out any) int {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp, err := c.Post(e.srv.URL+"/api/v1/auth/dev-login", "", nil); err == nil {
		resp.Body.Close()
	}
	resp, err := c.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(out)
	return resp.StatusCode
}

// A reviewing agent checks the work in the task's worktree, read only plus
// tests; its verdict decides. A failed review with on_fail=ask blocks the
// task and wakes the supervisor instead of reworking.
func TestAgentReview(t *testing.T) {
	te := newTaskEnv(t)
	_, stop := te.startDaemonWith("answer")
	defer stop()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "看一下", Detail: "检查 README", Dispatch: true, Review: event.ReviewAgent}))
	if err != nil {
		t.Fatal(err)
	}
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	if done.Status != event.TaskDone || !strings.Contains(done.ReviewNote, "测试都通过了") {
		t.Fatalf("task: %+v", done)
	}
	runs := te.runs(tk.ID)
	if len(runs) != 2 || runs[1].Purpose != event.PurposeReview || runs[1].Persona != "审查" || runs[1].Workdir != runs[0].Workdir {
		t.Fatalf("runs: %+v", runs)
	}
	if len(te.reviewer.requests()) != 0 {
		t.Fatal("the reviewer model was asked")
	}
	stop()

	_, stop = te.startDaemonWith("answer", "FAKE_ACP_VERDICT=fail")
	defer stop()
	tk2, _ := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "再看一下", Dispatch: true, Review: event.ReviewAgent, OnFail: event.OnFailAsk}))
	blocked := te.waitStatus(tk2.ID, event.TaskBlocked, event.TaskDone)
	if blocked.Status != event.TaskBlocked || !strings.Contains(blocked.StatusReason, "缺少测试") || len(te.runs(tk2.ID)) != 2 {
		t.Fatalf("task: %+v runs %d", blocked, len(te.runs(tk2.ID)))
	}
	te.waitFor("the follow-up for the supervisor", func() bool {
		evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
		for _, ev := range evs {
			var p event.TriggerPayload
			if ev.Type == event.TriggerFired && ev.Decode(&p) == nil && strings.HasPrefix(p.Key, "review-failed:") && strings.Contains(p.Content, tk2.ID) {
				return true
			}
		}
		return false
	})
}

// In a directory without git a task works in place after a snapshot; its
// diff is reviewed, and task_restore puts the directory back.
func TestInPlaceTaskAndRestore(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "notes")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "todo.txt"), []byte("old\n"), 0o644)
	name := "notes"
	nodes := []event.ProjectNode{{NodeID: te.cfg.NodeID, Path: dir}}
	p, err := te.tasks.PutProject(ctx, task.ProjectInput{Name: &name, Nodes: &nodes}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: p.ID, Title: "加 hello", Place: event.PlaceInPlace,
		Review: event.ReviewModel, OnFail: event.OnFailAsk, Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed)
	if done.Status != event.TaskDone || done.BaseCommit == "" || done.Branch != "" || !strings.Contains(done.StatusReason, "可以撤销") {
		t.Fatalf("task: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatal("the task did not work in the directory")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("the directory got a .git")
	}
	if reqs := te.reviewer.requests(); len(reqs) != 1 || !strings.Contains(reqs[0].Messages[0].PlainText(), "hello.txt") {
		t.Fatalf("the reviewer did not see the in-place diff: %+v", reqs)
	}
	restored := te.gitOp(task.GitInput{Op: "restore", TaskID: tk.ID, ThreadID: te.main})
	if a := te.approveNext(tk.ID); a.Category != "delete" {
		t.Fatalf("restore approval: %+v", a)
	}
	if r := <-restored; r.Status != event.RunStatusSucceeded || !strings.Contains(task.GitReport(r), "恢复到任务开始前") {
		t.Fatalf("restore: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("the task's file is still there")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "todo.txt")); string(b) != "old\n" {
		t.Fatalf("todo.txt: %q", b)
	}
	te.waitFor("the restored state", func() bool { return te.task(tk.ID).GitState == event.GitRestored })
	// Git operations on a task's branch make no sense here.
	if _, err := te.tasks.StartGit(ctx, task.GitInput{Op: "merge", TaskID: tk.ID, Into: "main"}); err == nil {
		t.Fatal("merged an in-place task")
	}
}

// The consultant reads the project and answers, keeps its session across
// questions, refuses to change anything, stops when idle and picks the
// session up again; a task grown from the discussion takes the session.
func TestConsultantDiscussionAndHandoff(t *testing.T) {
	node.ConsultIdle = 1500 * time.Millisecond
	defer func() { node.ConsultIdle = 0 }()
	te := newTaskEnv(t)
	prompts := filepath.Join(t.TempDir(), "prompts.txt")
	_, stop := te.startDaemonWith("answer", "FAKE_ACP_LOAD=1", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	ctx := context.Background()

	out, err := te.callTool("consult", map[string]any{"project": "demo", "question": "登录是怎么做的？"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "第 1 个问题") || !strings.Contains(out, "读了 README.md") || !strings.Contains(out, "不要复述") {
		t.Fatalf("first answer: %s", out)
	}
	cons := te.messages(event.KindConsult)
	if len(cons) != 1 || cons[0].ReplyTo != 7 || cons[0].Source == nil || cons[0].Source.Agent != "fake" || cons[0].Source.Project != "demo" {
		t.Fatalf("consult messages: %+v", cons)
	}
	cur, err := te.log.Store.Consultant(ctx, "p1")
	if err != nil || cur.Status != event.RunStatusRunning || cur.Purpose != event.PurposeConsult || cur.Kind != "in_place" {
		t.Fatalf("consultant %+v %v", cur, err)
	}
	if n, _ := te.log.Store.Node(ctx, te.cfg.NodeID); te.hub.Busy(ctx, n.ID) != 0 {
		t.Fatal("the consultant takes a slot")
	}

	// A follow-up question goes to the same session.
	out, err = te.callTool("consult", map[string]any{"project": "demo", "question": "那注册呢？"}, 9)
	if err != nil || !strings.Contains(out, "第 2 个问题") {
		t.Fatalf("second answer: %s %v", out, err)
	}
	if r, _ := te.log.Store.Consultant(ctx, "p1"); r.ID != cur.ID {
		t.Fatal("a second session was opened for a follow-up")
	}
	// It cannot change anything, and nobody is asked.
	out, err = te.callTool("consult", map[string]any{"project": "demo", "question": "顺手改一下 README"}, 11)
	if err != nil || !strings.Contains(out, "不能改文件") {
		t.Fatalf("edit answer: %s %v", out, err)
	}
	if as, _ := te.log.Store.Approvals(ctx, "pending", ""); len(as) != 0 {
		t.Fatalf("approvals for a read-only session: %+v", as)
	}
	if b, _ := os.ReadFile(filepath.Join(te.repo, "README.md")); string(b) != "# demo\n" {
		t.Fatalf("README changed: %q", b)
	}

	// Idle: the agent stops, the session id stays for the next question.
	ended := te.ended(cur.ID)
	if ended.Status != event.RunStatusSucceeded || ended.Session != "fake-session" {
		t.Fatalf("idle consultant: %+v", ended)
	}
	out, err = te.callTool("consult", map[string]any{"project": "demo", "question": "我们刚才说到哪了？"}, 13)
	if err != nil || !strings.Contains(out, "接着之前的会话") {
		t.Fatalf("resumed answer: %s %v", out, err)
	}
	resumed, _ := te.log.Store.Consultant(ctx, "p1")
	if resumed.ID == cur.ID || resumed.ResumeSession != "fake-session" {
		t.Fatalf("resumed run %+v", resumed)
	}
	env := strings.Join(te.tasks.Environment(ctx, te.main), "\n")
	if !strings.Contains(env, "顾问讨论") {
		t.Fatalf("environment: %s", env)
	}

	// The discussion becomes a task: the session goes with it.
	tk, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "按讨论改", Place: event.PlaceInPlace,
		Review: event.ReviewNone, FromConsult: true, Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Handoff == nil || tk.Handoff.SessionID != "fake-session" || tk.NodePref != te.cfg.NodeID || tk.AgentPref != "fake" || !strings.Contains(tk.Handoff.Note, "第 1 个问题") {
		t.Fatalf("handoff: %+v %+v", tk, tk.Handoff)
	}
	if _, err := te.log.Store.Consultant(ctx, "p1"); err == nil {
		t.Fatal("the consultant is still open after the handoff")
	}
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked)
	if done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	work := te.runs(tk.ID)[0]
	if work.ResumeSession != "fake-session" || !strings.Contains(work.Prompt, "之前的讨论") || !strings.Contains(work.Summary, "接着之前的会话") {
		t.Fatalf("work run: %+v", work)
	}
	if _, err := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "x", FromConsult: true, Actor: event.ActorSupervisor}); err == nil {
		t.Fatal("a handoff without a discussion")
	}
}

// project_read reads, lists and searches a project directly; git_status
// says where the checkout stands.
func TestProjectReadAndGitStatus(t *testing.T) {
	te := newTaskEnv(t)
	te.setConfig(func(c *node.Config) { c.Workspaces = []string{filepath.Dir(te.repo)} })
	defer te.startDaemon("write")()
	os.WriteFile(filepath.Join(te.repo, "login.go"), []byte("package x\n\nfunc Login() {}\n"), 0o644)

	out, err := te.callTool("project_read", map[string]any{"project": "demo", "op": "read", "path": "README.md"}, 0)
	if err != nil || !strings.Contains(out, "# demo") {
		t.Fatalf("read: %s %v", out, err)
	}
	out, err = te.callTool("project_read", map[string]any{"project": "demo", "op": "list"}, 0)
	if err != nil || !strings.Contains(out, "login.go") || strings.Contains(out, ".git") {
		t.Fatalf("list: %s %v", out, err)
	}
	out, err = te.callTool("project_read", map[string]any{"project": "demo", "op": "grep", "pattern": `func Login`}, 0)
	if err != nil || !strings.Contains(out, "login.go:3: func Login() {}") {
		t.Fatalf("grep: %s %v", out, err)
	}
	if _, err := te.callTool("project_read", map[string]any{"project": "demo", "op": "read", "path": "../../etc/passwd"}, 0); err == nil {
		t.Fatal("read outside the project")
	}
	out, err = te.callTool("git_status", map[string]any{"project": "demo"}, 0)
	if err != nil || !strings.Contains(out, "当前分支 main") || !strings.Contains(out, "未跟踪") || !strings.Contains(out, "origin") {
		t.Fatalf("git_status: %s %v", out, err)
	}
	// Git operations name their branches.
	if _, err := te.callTool("git_push", map[string]any{"project": "demo"}, 0); err == nil {
		t.Fatal("push without a branch")
	}
}

// A pull request is one operation among others: push the task branch and
// open it against the named base. Discarding deletes a branch for good.
func TestPullRequestAndDiscard(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	a, _ := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "Add hello", Dispatch: true, Review: event.ReviewNone}))
	te.waitStatus(a.ID, event.TaskDone)
	if _, err := te.tasks.StartGit(ctx, task.GitInput{Op: "pr", TaskID: a.ID}); err == nil {
		t.Fatal("a pull request without a base")
	}
	pr := te.gitOp(task.GitInput{Op: "pr", TaskID: a.ID, Base: "main", ThreadID: te.main})
	if ap := te.approveNext(a.ID); ap.Category != "git-remote" || !strings.Contains(ap.Detail, "--base main") {
		t.Fatalf("pr approval: %+v", ap)
	}
	if r := <-pr; r.Status != event.RunStatusSucceeded || r.PRURL != "https://github.com/u/demo/pull/1" {
		t.Fatalf("pr: %+v", r)
	}
	te.waitFor("the pr state", func() bool {
		x := te.task(a.ID)
		return x.GitState == event.GitPR && x.PRURL != ""
	})
	if gh, _ := os.ReadFile(te.ghLog); !strings.Contains(string(gh), "--base main") {
		t.Fatalf("gh: %s", gh)
	}

	b, _ := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "Try something", Dispatch: true, Review: event.ReviewNone}))
	te.waitStatus(b.ID, event.TaskDone)
	branch := te.task(b.ID).Branch
	discard := te.gitOp(task.GitInput{Op: "discard", TaskID: b.ID, ThreadID: te.main})
	if ap := te.approveNext(b.ID); ap.Category != "irreversible" {
		t.Fatalf("discard approval: %+v", ap)
	}
	if r := <-discard; r.Status != event.RunStatusSucceeded {
		t.Fatalf("discard: %+v", r)
	}
	if out, _ := exec.Command("git", "-C", te.repo, "branch", "--list", branch).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Fatal("the branch is still there")
	}
	te.waitFor("the discarded state", func() bool { return te.task(b.ID).GitState == event.GitDiscarded })
	if _, err := te.tasks.Dispatch(ctx, b.ID, "", "", event.ActorUser); err == nil {
		t.Fatal("a discarded task was dispatched again")
	}
}

// Two questions in the same turn (the supervisor's tool calls run in
// parallel) go to one consultant session, one after the other.
func TestParallelQuestionsShareTheConsultant(t *testing.T) {
	te := newTaskEnv(t)
	_, stop := te.startDaemonWith("answer", "FAKE_ACP_LOAD=1")
	defer stop()
	outs := make(chan string, 2)
	for _, q := range []string{"登录怎么做的？", "注册怎么做的？"} {
		go func(q string) {
			out, err := te.callTool("consult", map[string]any{"project": "demo", "question": q}, 5)
			if err != nil {
				t.Error(err)
			}
			outs <- out
		}(q)
	}
	got := <-outs + "\n" + <-outs
	if !strings.Contains(got, "第 1 个问题") || !strings.Contains(got, "第 2 个问题") {
		t.Fatalf("answers:\n%s", got)
	}
	runs, _ := te.log.Store.Runs(context.Background(), store.RunFilter{ProjectID: "p1", Purpose: event.PurposeConsult})
	if len(runs) != 1 {
		t.Fatalf("%d consultant runs", len(runs))
	}
}

// An in-place task in a git checkout: "提交推送" commits only the task's
// files on the checkout's current branch, then pushes that branch.
func TestInPlaceCommitAndPush(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("edit")()
	ctx := context.Background()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", te.repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("push", "-q", "-u", "origin", "main")
	git("checkout", "-qb", "feature-x")
	os.WriteFile(filepath.Join(te.repo, "mine.txt"), []byte("the user's own work\n"), 0o644)

	tk, err := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "加 hello", Place: event.PlaceInPlace,
		Review: event.ReviewNone, Dispatch: true, Actor: event.ActorSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskFailed); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	r := <-te.gitOp(task.GitInput{Op: "commit", TaskID: tk.ID, ThreadID: te.main})
	if r.Status != event.RunStatusSucceeded || r.GitResult == nil || r.GitResult.Branch != "feature-x" {
		t.Fatalf("commit: %+v %+v", r, r.GitResult)
	}
	if files := git("show", "--name-only", "--format=", "HEAD"); files != "hello.txt" {
		t.Fatalf("committed: %q", files)
	}
	if st := git("status", "--porcelain"); !strings.Contains(st, "?? mine.txt") {
		t.Fatalf("the user's file was touched: %s", st)
	}
	pushed := te.gitOp(task.GitInput{Op: "push", ProjectID: "p1", Branch: "feature-x", ThreadID: te.main})
	te.waitFor("the push approval", func() bool {
		as, _ := te.log.Store.Approvals(ctx, "pending", "")
		for _, x := range as {
			if x.Category == "git-remote" && strings.Contains(x.Detail, "git push origin feature-x") {
				te.hub.Decide(ctx, x.ID, "allow", "")
				return true
			}
		}
		return false
	})
	if pr := <-pushed; pr.Status != event.RunStatusSucceeded || !strings.Contains(task.GitReport(pr), "新分支") {
		t.Fatalf("push: %+v", pr)
	}
	if out, _ := exec.Command("git", "-C", te.origin, "log", "--format=%s", "feature-x").Output(); !strings.Contains(string(out), "加 hello") {
		t.Fatalf("origin feature-x: %s", out)
	}
}

package node_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// Decision 0025: a rework on the same machine and agent goes on in the
// task's earlier agent session with only what is new, and every report
// about the task wakes the supervisor with a task trigger.
func TestReworkContinuesTheSessionAndWakesTheSupervisor(t *testing.T) {
	te := newTaskEnv(t)
	prompts := filepath.Join(t.TempDir(), "prompts.log")
	_, stop := te.startDaemonWith("edit", "FAKE_ACP_LOAD=1", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	ctx := context.Background()
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "Add hello", Detail: "加一个 hello 文件", Dispatch: true, Review: event.ReviewUser}))
	if err != nil {
		t.Fatal(err)
	}
	te.waitFor("the user review", func() bool { return task.UserReviewing(te.task(tk.ID)) })
	first := te.runs(tk.ID)[0]
	if first.Session == "" {
		t.Fatalf("the work run did not record its session: %+v", first)
	}
	if !strings.Contains(first.Prompt, "## 结果") {
		t.Fatalf("the work prompt asks for no result section: %s", first.Prompt)
	}
	te.waitFor("a task trigger for the review report", func() bool {
		for _, m := range te.messages(event.KindTrigger) {
			if strings.Contains(m.Content, "【任务进展】") && strings.Contains(m.Content, "Add hello") {
				return true
			}
		}
		return false
	})

	if _, err := te.tasks.Verdict(ctx, tk.ID, false, "README 也要改", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	te.waitFor("the rework run", func() bool {
		rs := te.runs(tk.ID)
		return len(rs) == 2 && rs[1].Status == event.RunStatusSucceeded
	})
	second := te.runs(tk.ID)[1]
	if second.ResumeSession != first.Session || !strings.Contains(second.ContinuePrompt, "README 也要改") ||
		!strings.Contains(second.ContinuePrompt, "继续这个任务") {
		t.Fatalf("rework run: resume %q continue %q", second.ResumeSession, second.ContinuePrompt)
	}
	log, _ := os.ReadFile(prompts)
	sent := strings.Split(string(log), "\n---\n")
	var last string
	for _, p := range sent {
		if strings.TrimSpace(p) != "" && !strings.HasPrefix(p, "set ") {
			last = p
		}
	}
	if !strings.Contains(last, "继续这个任务") || strings.Contains(last, "sous 为这个任务新建的 git worktree") {
		t.Fatalf("the agent got the whole task again: %s", last)
	}
}

// Decision 0025: a sub-agent reaches sous through its own MCP server. Its
// progress shows on the run, its question wakes the supervisor, the
// supervisor's run_message is the answer it waits for, and its handed-in
// result becomes the report's result section.
func TestAgentAsksTheSupervisor(t *testing.T) {
	te := newTaskEnv(t)
	d, stop := te.startDaemonWith("ask", acptest.EnvSousMCP+"=1")
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.ServeIPC(ctx, node.NewLogRing(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "改端口", Detail: "把服务端口改掉", Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	te.waitFor("the agent's question for the supervisor", func() bool {
		for _, m := range te.messages(event.KindTrigger) {
			if strings.Contains(m.Content, "【agent 的问题】") && strings.Contains(m.Content, "8080 还是 3000") && strings.Contains(m.Content, tk.ID) {
				return true
			}
		}
		return false
	})
	if out, err := te.callTool("run_message", map[string]any{"task": tk.ID, "text": "用 3000"}, 0); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked)
	if done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	r := te.runs(tk.ID)[0]
	got, _ := os.ReadFile(filepath.Join(r.Workdir, "answer.txt"))
	if !strings.Contains(string(got), "用 3000") {
		t.Fatalf("the agent got %q", got)
	}
	res, ok := task.ParseResult(r.Summary)
	if !ok || res.Done != "按回答写了 answer.txt" || res.Verified != "没有验证" {
		t.Fatalf("result section: %+v in %q", res, r.Summary)
	}
	evs, _ := te.log.Store.RunEvents(ctx, r.ID)
	var progress, asked bool
	for _, ev := range evs {
		var p event.RunUpdatePayload
		if ev.Type == event.RunUpdate && ev.Decode(&p) == nil {
			progress = progress || strings.Contains(p.Text, "进度：看完了代码")
			asked = asked || strings.Contains(p.Text, "在问主管：")
		}
	}
	if !progress || !asked {
		t.Fatalf("run updates: progress %v asked %v", progress, asked)
	}
	var report string
	for _, m := range te.messages(event.KindReport) {
		if strings.Contains(m.Content, "改端口") {
			report = m.Content
		}
	}
	if !strings.Contains(report, "- 做了：按回答写了 answer.txt\n- 验证：没有验证") {
		t.Fatalf("report: %q", report)
	}
}

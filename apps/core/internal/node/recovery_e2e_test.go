package node_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/inspector"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ipc"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

func (te *taskEnv) firstRun(taskID string) store.Run {
	te.t.Helper()
	te.waitFor("a run of the task", func() bool { return len(te.runs(taskID)) > 0 })
	return te.runs(taskID)[0]
}

func (te *taskEnv) messages(kind string) []store.Message {
	msgs, _ := te.log.Store.Messages(context.Background(), te.main, 0, 200)
	var out []store.Message
	for _, m := range msgs {
		if m.Kind == kind {
			out = append(out, m)
		}
	}
	return out
}

// Acceptance scenario 5: a run that calls the same tool over and over is
// found and paused by the inspector in one round, and the user is told.
func TestInspectorPausesLoopingRun(t *testing.T) {
	te := newTaskEnv(t)
	defer te.startDaemon("loop")()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ThreadID: te.main, Title: "查一下依赖", Dispatch: true, Actor: event.ActorSupervisor})
	run := te.firstRun(tk.ID)
	te.waitFor("the agent to loop", func() bool {
		evs, _ := te.log.Store.RunEvents(ctx, run.ID)
		n := 0
		for _, ev := range evs {
			var p event.RunUpdatePayload
			if ev.Type == event.RunUpdate && ev.Decode(&p) == nil && p.Kind == "tool" && p.Title == "Read go.mod" {
				n++
			}
		}
		return n >= 8
	})

	verdict := &verdicts{answers: []string{`{"severity":"warn","action":"pause","reason":"agent 一直在重复读 go.mod，已经暂停"}`}}
	in := inspector.New(te.log, te.hub, verdict, "i", inspector.Options{})
	found := in.Inspect(ctx)
	if len(found) != 1 || found[0].Rule != inspector.RuleRepeat || found[0].Action != event.ActionPause || !found[0].Done {
		t.Fatalf("findings: %+v", found)
	}
	if reqs := verdict.requests(); len(reqs) != 1 || !strings.Contains(reqs[0].Messages[0].PlainText(), "Read go.mod") {
		t.Fatalf("inspector model input: %+v", reqs)
	}
	if r := te.run(run.ID); !r.Paused || r.Status != event.RunStatusRunning {
		t.Fatalf("run not paused: %+v", r)
	}
	// The engine notes the pause on the task asynchronously.
	te.waitFor("the task to show the pause", func() bool {
		return strings.HasPrefix(te.task(tk.ID).StatusReason, task.PausedPrefix)
	})
	if notes := te.messages(event.KindInspector); len(notes) != 1 || !strings.Contains(notes[0].Content, "已经暂停") {
		t.Fatalf("main thread notes: %+v", notes)
	}
	// The finding sends a push notification.
	evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
	pushed := false
	for _, ev := range evs {
		pushed = pushed || ev.Type == event.InspectorFinding && event.PushKind(ev) == event.PushInspector
	}
	if !pushed {
		t.Fatal("the finding was not pushed")
	}
	// The agent stops calling tools while paused, and the next round leaves it be.
	time.Sleep(300 * time.Millisecond)
	before := len(toolEvents(te, run.ID))
	time.Sleep(300 * time.Millisecond)
	if after := len(toolEvents(te, run.ID)); after != before {
		t.Fatalf("the paused agent kept working: %d -> %d tool events", before, after)
	}
	if again := in.Inspect(ctx); len(again) != 0 {
		t.Fatalf("a paused run was flagged again: %+v", again)
	}
	te.waitFor("the paused status line", func() bool {
		log, _ := os.ReadFile(te.hub.LogPath(run.ID))
		return strings.Contains(string(log), "已暂停")
	})

	// The user resumes it; the agent finishes.
	if err := te.hub.Resume(ctx, run.ID, "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	if r := te.ended(run.ID); r.Status != event.RunStatusSucceeded || !strings.Contains(r.Summary, "换个办法") {
		t.Fatalf("run after resume: %+v", r)
	}
	if k := te.waitStatus(tk.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked); k.Status != event.TaskDone {
		t.Fatalf("task: %+v", k)
	}
}

func toolEvents(te *taskEnv, runID string) []event.Event {
	evs, _ := te.log.Store.RunEvents(context.Background(), runID)
	var out []event.Event
	for _, ev := range evs {
		if ev.Type == event.RunUpdate && strings.Contains(string(ev.Payload), `"tool"`) {
			out = append(out, ev)
		}
	}
	return out
}

// A message to a paused run resumes it with that message.
func TestMessageResumesPausedRun(t *testing.T) {
	te := newTaskEnv(t)
	prompts := filepath.Join(t.TempDir(), "prompts.txt")
	_, stop := te.startDaemonWith("loop", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{Title: "x", Dispatch: true, Actor: event.ActorSupervisor})
	run := te.firstRun(tk.ID)
	te.waitFor("the agent to start looping", func() bool { return len(toolEvents(te, run.ID)) > 2 })
	if err := te.hub.Pause(ctx, run.ID, "用户暂停", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	if err := te.hub.MessageAs(ctx, run.ID, "别再读 go.mod 了，直接结束", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	if r := te.ended(run.ID); r.Status != event.RunStatusSucceeded || r.Paused {
		t.Fatalf("run: %+v", r)
	}
	got, _ := os.ReadFile(prompts)
	if !strings.Contains(string(got), "别再读 go.mod 了") {
		t.Fatalf("prompts: %s", got)
	}
	ms, _ := te.log.Store.RunMessages(ctx, run.ID)
	if len(ms) != 1 || !ms[0].Delivered {
		t.Fatalf("run messages: %+v", ms)
	}
}

// Acceptance scenario 4, when the agent can load its session: the daemon
// stops mid-run; the next daemon resumes the same run in the same session,
// and a follow-up sent while the machine was away reaches the agent once.
func TestDaemonRestartResumesSession(t *testing.T) {
	te := newTaskEnv(t)
	prompts := filepath.Join(t.TempDir(), "prompts.txt")
	_, stop := te.startDaemonWith("slow", "FAKE_ACP_LOAD=1", "FAKE_ACP_PROMPTS="+prompts)
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "Add hello", Dispatch: true, Actor: event.ActorSupervisor})
	run := te.firstRun(tk.ID)
	te.waitFor("the agent to work", func() bool {
		log, _ := os.ReadFile(te.hub.LogPath(run.ID))
		return strings.Contains(string(log), "我先看一下仓库")
	})
	stop() // the daemon goes away in the middle of the turn
	te.waitFor("the machine to go offline", func() bool { return !te.hub.Online(te.cfg.NodeID) })
	if err := te.hub.MessageAs(ctx, run.ID, "顺便在 README 里提一句", event.ActorUser); err != nil {
		t.Fatal(err)
	}

	_, stop = te.startDaemonWith("slow", "FAKE_ACP_LOAD=1", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	te.approveNext(tk.ID) // the push
	done := te.waitStatus(tk.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked)
	if done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	runs := te.runs(tk.ID)
	if runs[0].ID != run.ID || runs[0].Status != event.RunStatusSucceeded || !strings.Contains(runs[0].Summary, "从中断的地方继续") {
		t.Fatalf("the run was not resumed in place: %+v", runs)
	}
	got, _ := os.ReadFile(prompts)
	if n := strings.Count(string(got), "顺便在 README 里提一句"); n != 1 {
		t.Fatalf("the follow-up reached the agent %d times:\n%s", n, got)
	}
	if !strings.Contains(string(got), "sous 在这台电脑上重启过") {
		t.Fatalf("no resume note:\n%s", got)
	}
	ms, _ := te.log.Store.RunMessages(ctx, run.ID)
	if len(ms) != 1 || !ms[0].Delivered {
		t.Fatalf("run messages: %+v", ms)
	}
	// Replayed history from session/load is not reported again.
	logText, _ := os.ReadFile(te.hub.LogPath(run.ID))
	if strings.Contains(string(logText), "回放的旧消息") {
		t.Fatal("the session replay was reported as new output")
	}
	if entries, _ := os.ReadDir(filepath.Join(te.root, "runs")); len(entries) != 0 {
		t.Fatalf("saved run state left behind: %v", entries)
	}
}

// Acceptance scenario 4, when the agent cannot load its session: the run
// is interrupted, the task restarts once with a recovery note, and the
// user is told.
func TestInterruptedRunRestartsWithRecoveryNote(t *testing.T) {
	te := newTaskEnv(t)
	prompts := filepath.Join(t.TempDir(), "prompts.txt")
	_, stop := te.startDaemonWith("write", "FAKE_ACP_PROMPTS="+prompts)
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ThreadID: te.main, ProjectID: "p1", Title: "Add hello", Dispatch: true, Actor: event.ActorSupervisor})
	run := te.firstRun(tk.ID)
	te.waitFor("the agent to ask", func() bool {
		as, _ := te.log.Store.Approvals(ctx, "pending", run.ID)
		return len(as) > 0
	})
	te.hub.MessageAs(ctx, run.ID, "测试也要加上", event.ActorUser)
	time.Sleep(200 * time.Millisecond)
	stop()

	_, stop = te.startDaemonWith("edit", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	if r := te.ended(run.ID); r.Status != event.RunStatusInterrupted {
		t.Fatalf("first run: %+v", r)
	}
	te.waitFor("a second run", func() bool { return len(te.runs(tk.ID)) >= 2 })
	second := te.runs(tk.ID)[1]
	if second.Purpose != event.PurposeWork || !strings.Contains(second.RecoveryNote, "恢复说明") || !strings.Contains(second.RecoveryNote, "测试也要加上") {
		t.Fatalf("second run: %+v", second)
	}
	te.approveNext(tk.ID) // the push
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskFailed, event.TaskBlocked); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	got, _ := os.ReadFile(prompts)
	if !strings.Contains(string(got), "【恢复说明】") {
		t.Fatalf("the agent did not get the recovery note:\n%s", got)
	}
	reports := te.messages(event.KindReport)
	told := false
	for _, m := range reports {
		told = told || strings.Contains(m.Content, "中断")
	}
	evs, _ := te.log.Store.EventsSince(ctx, 0, 5000)
	pushed := false
	for _, ev := range evs {
		if ev.Type == event.MessageAssistant && strings.Contains(string(ev.Payload), "中断") {
			pushed = pushed || event.PushKind(ev) == event.PushReport
		}
	}
	if !told || !pushed {
		t.Fatalf("the user was not told (report %v, push %v)", told, pushed)
	}
}

// A second interruption after the automatic restart fails the task.
func TestSecondInterruptionFailsTask(t *testing.T) {
	te := newTaskEnv(t)
	_, stop := te.startDaemonWith("slow")
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{Title: "慢任务", Dispatch: true, Actor: event.ActorSupervisor})
	first := te.firstRun(tk.ID)
	te.waitFor("the first run to start", func() bool { return te.run(first.ID).Status == event.RunStatusRunning })
	stop()
	_, stop = te.startDaemonWith("slow")
	te.waitFor("the restarted run", func() bool {
		rs := te.runs(tk.ID)
		return len(rs) == 2 && rs[1].Status == event.RunStatusRunning
	})
	stop()
	_, stop = te.startDaemonWith("slow")
	defer stop()
	failed := te.waitStatus(tk.ID, event.TaskFailed)
	if !strings.Contains(failed.StatusReason, "中断") || len(te.runs(tk.ID)) != 2 {
		t.Fatalf("task: %+v, runs %d", failed, len(te.runs(tk.ID)))
	}
}

// `sous chat` talks to the Hub through the daemon's socket.
func TestIPCChat(t *testing.T) {
	e := newEnv(t)
	d, stop := e.startDaemonWith("write")
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.ServeIPC(ctx, node.NewLogRing(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	c, err := ipc.Dial(ipc.Path(e.root))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var st node.Status
	if err := c.Call(ctx, "status", nil, &st); err != nil || !st.HubConnected || st.NodeID != e.cfg.NodeID {
		t.Fatalf("status %+v %v", st, err)
	}
	sub, _ := ipc.Dial(ipc.Path(e.root))
	defer sub.Close()
	if err := sub.Call(ctx, "events.subscribe", map[string]int64{"since": 0}, nil); err != nil {
		t.Fatal(err)
	}
	var threads struct {
		Threads []store.Thread
	}
	if err := c.Call(ctx, "threads.list", nil, &threads); err != nil || len(threads.Threads) != 1 || threads.Threads[0].Kind != "main" {
		t.Fatalf("threads %+v %v", threads, err)
	}
	var sent struct {
		ThreadID  string `json:"thread_id"`
		MessageID string `json:"message_id"`
	}
	if err := c.Call(ctx, "chat.send", map[string]string{"content": "从终端发的"}, &sent); err != nil || sent.ThreadID != e.main {
		t.Fatalf("send %+v %v", sent, err)
	}
	msgs, _ := e.log.Store.Messages(ctx, e.main, 0, 10)
	if len(msgs) != 1 || msgs[0].Content != "从终端发的" {
		t.Fatalf("messages: %+v", msgs)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n := <-sub.Notifications():
			var ev event.Event
			if n.Method == "event" && json.Unmarshal(n.Params, &ev) == nil && ev.Type == event.MessageUser && strings.Contains(string(ev.Payload), sent.MessageID) {
				return
			}
		case <-deadline:
			t.Fatal("the event did not reach the subscriber")
		}
	}
}

// A machine that was offline when a task closed removes the task's
// worktree when it comes back.
func TestWorktreeRemovedWhenMachineReturns(t *testing.T) {
	te := newTaskEnv(t)
	ctx := context.Background()
	tk, _ := te.tasks.Create(ctx, task.Input{ProjectID: "p1", Title: "旧任务", Actor: event.ActorSupervisor})
	ws, err := workspace.PrepareTask(ctx, te.root, te.repo, tk.ID, "", "old")
	if err != nil {
		t.Fatal(err)
	}
	q := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{NodeID: te.cfg.NodeID, Agent: "fake", Kind: "code", RepoPath: te.repo, Prompt: "x"})
	q.RunID, q.TaskID = ids.New(), tk.ID
	end := event.New(event.RunEnded, event.ActorSystem, "", event.RunEndedPayload{Status: "cancelled"})
	end.RunID, end.TaskID = q.RunID, tk.ID
	st := event.New(event.TaskStatus, event.ActorUser, "", event.TaskStatusPayload{From: "planned", To: event.TaskCancelled})
	st.TaskID = tk.ID
	if _, err := te.log.Append(ctx, q, end, st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.Dir); err != nil {
		t.Fatal(err)
	}
	defer te.startDaemon("edit")()
	te.waitFor("the worktree to be removed", func() bool {
		_, err := os.Stat(ws.Dir)
		return os.IsNotExist(err)
	})
}

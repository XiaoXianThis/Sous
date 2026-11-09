package task

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Parallel code tasks of one project that change the same files collide
// at their pull requests. When a run's diff touches files another open
// task of the project already changed, the supervisor hears about it once
// per pair (work/specs/multi-machine-sync, R5) and decides: let them be,
// or make one wait for the other (depends_on). Nothing is blocked here.

// diffFiles lists the files a run's diff changes.
func (e *Engine) diffFiles(runID string) map[string]bool {
	f, err := os.Open(e.Nodes.DiffPath(runID))
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "diff --git a/") {
			continue
		}
		// diff --git a/<path> b/<path>; paths with spaces still split at " b/".
		rest := strings.TrimPrefix(line, "diff --git a/")
		if i := strings.LastIndex(rest, " b/"); i >= 0 {
			out[rest[:i]] = true
			out[rest[i+3:]] = true
		}
	}
	return out
}

// lastDiff is the files of the task's latest work or rebase run that had a diff.
func (e *Engine) lastDiff(ctx context.Context, taskID string) map[string]bool {
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: taskID})
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if (r.Purpose == event.PurposeWork || r.Purpose == event.PurposeRebase) && r.DiffStat != "" {
			return e.diffFiles(r.ID)
		}
	}
	return nil
}

// checkOverlap compares the run's files with the project's other open
// code tasks.
func (e *Engine) checkOverlap(ctx context.Context, t store.Task, r store.Run) {
	if t.Place != event.PlaceWorktree || t.ProjectID == "" || r.DiffStat == "" {
		return
	}
	mine := e.diffFiles(r.ID)
	if len(mine) == 0 {
		return
	}
	others, err := e.store().Tasks(ctx, store.TaskFilter{ProjectID: t.ProjectID})
	if err != nil {
		return
	}
	for _, o := range others {
		if o.ID == t.ID || o.Place != event.PlaceWorktree || o.Status == event.TaskCancelled || o.Status == event.TaskFailed || worktreeDone(o) {
			continue
		}
		var both []string
		for f := range e.lastDiff(ctx, o.ID) {
			if mine[f] {
				both = append(both, f)
			}
		}
		if len(both) == 0 {
			continue
		}
		a, b := t.ID, o.ID
		if b < a {
			a, b = b, a
		}
		key := "overlap:" + a + ":" + b
		if at, err := e.store().TriggerFiredAt(ctx, key); err != nil || at > 0 {
			continue
		}
		sort.Strings(both)
		if len(both) > 10 {
			both = append(both[:10], fmt.Sprintf("…等 %d 个", len(both)))
		}
		content := fmt.Sprintf("同一个项目里两个任务改到了相同的文件，合并时可能冲突：\n- 任务 %s「%s」（%s）\n- 任务 %s「%s」（%s）\n重叠的文件：%s\n\n"+
			"判断要不要让其中一个等另一个合并之后再做（task_update 设 depends_on），或者确认它们改的是不同的地方、可以继续。"+
			"需要用户决定时用 notify 简短提醒一次；不需要做什么时调用 stay_silent。（这是 sous 的自动提醒，不是用户发的消息。）",
			t.ID, t.Title, t.Status, o.ID, o.Title, o.Status, strings.Join(both, "、"))
		main := store.Task{}
		e.followup(ctx, main, key, "并行任务改到了相同的文件", content)
	}
}

// followup wakes the supervisor with a note only it reads (a trigger in the
// task's thread, else the main thread), once per key.
func (e *Engine) followup(ctx context.Context, t store.Task, key, title, content string) {
	e.fire(ctx, t, event.TriggerFollowup, key, title, content)
}

// fire appends a trigger for the supervisor of a thread (the main thread
// when empty), once per key.
func (e *Engine) fire(ctx context.Context, t store.Task, kind, key, title, content string) {
	thread := t.OriginThreadID
	if at, err := e.store().TriggerFiredAt(ctx, key); err != nil || at > 0 {
		return
	}
	if thread == "" {
		var err error
		if thread, err = e.store().MainThreadID(ctx); err != nil {
			return
		}
	}
	ev := event.New(event.TriggerFired, event.ActorTrigger, thread, event.TriggerPayload{
		Kind: kind, Key: key, MessageID: ids.New(), Title: title, Content: content,
		At: time.Now().UnixMilli(), TopicID: e.topicOf(ctx, t),
	})
	ev.TaskID = t.ID
	if _, err := e.Log.Append(ctx, ev); err != nil {
		slog.Error("task: trigger", "key", key, "err", err)
	}
}

// tell is a task trigger whose content already says what to do.
func (e *Engine) tell(ctx context.Context, t store.Task, key, title, content string) {
	e.fire(ctx, t, event.TriggerTask, key, title, content)
}

// wakeNote ends every task trigger: what the supervisor is to do with it.
const wakeNote = "\n\n（这是 sous 的自动通知，不是用户发的消息；用户已经看到了上面的汇报。判断要不要接着推进：" +
	"有该做的下一步就直接做（派发、返工、换 agent 或机器、git 操作、部署）；需要用户拿主意时用 notify 简短提醒一次；" +
	"没有要做的事时调用 stay_silent，不要重复汇报的内容。）"

// wake tells the supervisor about a task's news (decision 0025): the
// report the user just got, or why the task stopped.
func (e *Engine) wake(ctx context.Context, t store.Task, key, title, content string) {
	e.fire(ctx, t, event.TriggerTask, key, title, content+wakeNote)
}

// topicOf is the topic of a task as stored now: a report earlier in the
// same pass may have just given it one.
func (e *Engine) topicOf(ctx context.Context, t store.Task) string {
	if t.ID == "" {
		return t.TopicID
	}
	if cur, err := e.store().Task(ctx, t.ID); err == nil {
		return cur.TopicID
	}
	return t.TopicID
}

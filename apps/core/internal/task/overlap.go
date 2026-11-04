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
	if t.Kind != event.TaskKindCode || t.ProjectID == "" || r.DiffStat == "" {
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
		if o.ID == t.ID || o.Kind != event.TaskKindCode || final(o.Status) || o.Status == event.TaskFailed {
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
		content := fmt.Sprintf("同一个项目里两个任务改到了相同的文件，PR 合并时可能冲突：\n- 任务 %s「%s」（%s）\n- 任务 %s「%s」（%s）\n重叠的文件：%s\n\n"+
			"判断要不要让其中一个等另一个合并之后再做（task_update 设 depends_on），或者确认它们改的是不同的地方、可以继续。"+
			"需要用户决定时用 notify 简短提醒一次；不需要做什么时调用 stay_silent。（这是 sous 的自动提醒，不是用户发的消息。）",
			t.ID, t.Title, t.Status, o.ID, o.Title, o.Status, strings.Join(both, "、"))
		thread, err := e.store().MainThreadID(ctx)
		if err != nil {
			return
		}
		ev := event.New(event.TriggerFired, event.ActorTrigger, thread, event.TriggerPayload{
			Kind: event.TriggerFollowup, Key: key, MessageID: ids.New(), Title: "并行任务改到了相同的文件", Content: content,
			At: time.Now().UnixMilli(),
		})
		if _, err := e.Log.Append(ctx, ev); err != nil {
			slog.Error("task: overlap notice", "err", err)
		}
	}
}

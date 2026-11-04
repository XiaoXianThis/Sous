package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/models"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	// maxDiffRunes bounds the diff the reviewer reads; the file list and the
	// stat are always complete.
	maxDiffRunes = 60000
	// approverGiveUp is after how many failed approver calls the approval
	// goes to the user.
	approverGiveUp = 3
)

// Advance is the effect handler for a run of a task that ended. Its payload
// is the run id.
func (e *Engine) Advance(ctx context.Context, eff store.Effect) error {
	r, err := e.store().Run(ctx, string(eff.Payload))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	e.mu.Lock()
	locked := true
	defer func() {
		if locked {
			e.mu.Unlock()
		}
	}()
	t, err := e.store().Task(ctx, r.TaskID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if final(t.Status) {
		return nil
	}
	if last := e.lastRun(ctx, t.ID, event.PurposeWork, event.PurposeRebase, event.PurposeDeliver); last.ID != r.ID {
		return nil // an older run; a newer one decides the task's state
	}

	if r.Status == event.RunStatusInterrupted {
		if ok, err := e.recover(ctx, t, r); ok || err != nil {
			return err
		}
	}
	if r.Purpose == event.PurposeDeliver {
		return e.afterDeliver(ctx, t, r)
	}
	switch r.Status {
	case event.RunStatusSucceeded:
		if r.Purpose == event.PurposeRebase {
			return e.queueNext(ctx, t, event.PurposeDeliver)
		}
		if t.Kind == event.TaskKindOps {
			// Operations change a server in place: there is no diff to
			// review and nothing to deliver. The summary is the report.
			if err := e.transition(ctx, t, event.TaskDone, "运维完成", "", event.ActorSystem); err != nil {
				return err
			}
			e.report(ctx, t, fmt.Sprintf("运维任务「%s」完成了。\n\n%s", t.Title, firstNonEmpty(r.Summary, "agent 没有写总结")))
			return nil
		}
		if t.Status != event.TaskReview {
			if err := e.transition(ctx, t, event.TaskReview, "等待验收", "", event.ActorSystem); err != nil {
				return err
			}
		}
		e.mu.Unlock() // the reviewer may take a while; do not hold up dispatching
		locked = false
		return e.review(ctx, t.ID, r)
	case event.RunStatusCancelled:
		return e.transition(ctx, t, event.TaskPlanned, "执行被取消："+firstNonEmpty(r.Error, "已取消"), "", event.ActorSystem)
	default:
		reason := firstNonEmpty(r.Error, r.Summary, "agent 没有完成")
		if r.Status == event.RunStatusInterrupted {
			reason = "执行被中断：" + reason
		}
		if err := e.transition(ctx, t, event.TaskFailed, reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, fmt.Sprintf("任务「%s」失败了：%s\n\n可以让我重试，或者换一个 agent、换一台机器再做。", t.Title, reason))
		return nil
	}
}

// recover starts the run again after it was interrupted (its machine's
// daemon restarted and could not resume the agent's session), once per
// dispatch. The new run gets a recovery note (see startRun).
func (e *Engine) recover(ctx context.Context, t store.Task, r store.Run) (bool, error) {
	since, _ := e.store().LastDispatch(ctx, t.ID)
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: t.ID})
	n := 0
	for _, x := range runs {
		if x.CreatedAt >= since && x.RecoveryNote != "" {
			n++
		}
	}
	if n >= MaxRecoveries {
		return false, nil
	}
	cause := firstNonEmpty(r.Error, "原因不明")
	if err := e.transition(ctx, t, event.TaskQueued, "执行被中断，正在重新开始", "", event.ActorSystem, waiting("", r.Purpose)); err != nil {
		return true, err
	}
	e.reportNotify(ctx, t, fmt.Sprintf("任务「%s」的执行被中断了：%s。\n\n我已经让它重新开始，agent 会先检查之前做到了哪一步，不会重复 push 之类的对外操作。", t.Title, cause))
	e.Sweep()
	return true, nil
}

// recoveryNote opens the prompt of a run that picks up after an
// interrupted one (design §9; SA-15, SA-16).
func (e *Engine) recoveryNote(ctx context.Context, prev store.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【恢复说明】这个任务上一次的执行被中断了（%s）。工作目录里可能留着那次的改动。\n"+
		"开始之前，先检查工作目录、git status、git log 和远端状态，确认之前做到了哪一步，再从那里继续；"+
		"不要重复已经完成的对外操作（比如 push、创建 PR、发消息、调用外部服务）。", firstNonEmpty(prev.Error, "原因不明"))
	if missed := e.Nodes.Undelivered(ctx, prev.ID); len(missed) > 0 {
		b.WriteString("\n\n中断之前用户还补充了这些要求，上一次没来得及看到：")
		for _, m := range missed {
			b.WriteString("\n- " + m.Text)
		}
	}
	return b.String()
}

// queueNext queues the task's next run and starts it if a machine is free.
func (e *Engine) queueNext(ctx context.Context, t store.Task, purpose string) error {
	if err := e.transition(ctx, t, event.TaskQueued, "等待派发", "", event.ActorSystem, waiting("", purpose)); err != nil {
		return err
	}
	e.Sweep()
	return nil
}

func (e *Engine) afterDeliver(ctx context.Context, t store.Task, r store.Run) error {
	switch r.Status {
	case event.RunStatusSucceeded:
		if r.Conflict {
			if e.countRuns(ctx, t.ID, event.PurposeRebase) < MaxRebases {
				return e.queueNext(ctx, t, event.PurposeRebase)
			}
			reason := "PR 和目标分支有冲突，自动 rebase 之后仍然没有解决"
			if err := e.transition(ctx, t, event.TaskBlocked, reason, r.PRURL, event.ActorSystem); err != nil {
				return err
			}
			e.report(ctx, t, fmt.Sprintf("任务「%s」受阻：%s。PR：%s", t.Title, reason, r.PRURL))
			return nil
		}
		if err := e.transition(ctx, t, event.TaskDone, "已创建 PR，等你评审合并", r.PRURL, event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, e.doneReport(ctx, t, r.PRURL))
		e.cleanup(ctx, t)
		return nil
	case event.RunStatusCancelled:
		reason := firstNonEmpty(r.Error, "交付被取消")
		if err := e.transition(ctx, t, event.TaskBlocked, reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, fmt.Sprintf("任务「%s」已经通过验收，但没有交付：%s。改动还在分支 %s 上，需要时让我再交付一次。", t.Title, reason, t.Branch))
		return nil
	default:
		reason := "交付失败：" + firstNonEmpty(r.Error, "未知原因")
		if err := e.transition(ctx, t, event.TaskBlocked, reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, fmt.Sprintf("任务「%s」已经通过验收，但%s。改动还在分支 %s 上。", t.Title, reason, t.Branch))
		return nil
	}
}

// review asks the reviewer whether the run did the task, then delivers,
// finishes, or sends the task back for another run.
func (e *Engine) review(ctx context.Context, taskID string, r store.Run) error {
	t, err := e.store().Task(ctx, taskID)
	if err != nil {
		return err
	}
	if r.Purpose == event.PurposeWork || r.Purpose == event.PurposeRebase {
		e.checkOverlap(ctx, t, r)
	}
	verdict, err := e.verdict(ctx, t, r)
	if err != nil {
		return err // the effect retries
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, err = e.store().Task(ctx, taskID); err != nil {
		return err
	}
	if t.Status != event.TaskReview {
		return nil // cancelled or changed by hand meanwhile
	}
	ev := event.New(event.TaskReviewed, event.ActorReviewer, t.OriginThreadID, verdict)
	ev.TaskID, ev.RunID = t.ID, r.ID
	if _, err := e.Log.Append(ctx, ev); err != nil {
		return err
	}
	t.ReviewNote = verdict.Reason
	if verdict.Pass {
		if t.Kind == event.TaskKindCode && r.DiffStat != "" {
			return e.queueNext(ctx, t, event.PurposeDeliver)
		}
		if err := e.transition(ctx, t, event.TaskDone, verdict.Reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, e.doneReport(ctx, t, ""))
		e.cleanup(ctx, t)
		return nil
	}
	if e.workRunsSinceDispatch(ctx, t.ID) >= MaxWorkRuns {
		reason := fmt.Sprintf("验收 %d 次都没有通过：%s", MaxWorkRuns, verdict.Reason)
		if err := e.transition(ctx, t, event.TaskBlocked, reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, fmt.Sprintf("任务「%s」受阻：%s\n\n需要你看一下改动，决定怎么继续。", t.Title, reason))
		return nil
	}
	return e.queueNext(ctx, t, event.PurposeWork)
}

// verdict runs the reviewer model on the task and the run's full diff.
func (e *Engine) verdict(ctx context.Context, t store.Task, r store.Run) (event.TaskReviewedPayload, error) {
	if !models.Usable(ctx, e.Reviewer.Provider) {
		return event.TaskReviewedPayload{Pass: true, Reason: "没有配置验收模型，自动通过。" + r.DiffStat}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## 任务\n%s\n\n%s\n\n", t.Title, t.Detail)
	fmt.Fprintf(&b, "## 交给 agent 的说明\n%s\n\n", r.Prompt)
	fmt.Fprintf(&b, "## agent 的总结\n%s\n\n", firstNonEmpty(r.Summary, "（没有总结）"))
	if t.Kind == event.TaskKindCode {
		diff, _ := os.ReadFile(e.Nodes.DiffPath(r.ID))
		fmt.Fprintf(&b, "## 改动统计\n%s\n\n", firstNonEmpty(r.DiffStat, "没有改动"))
		if files := diffFiles(string(diff)); len(files) > 0 {
			fmt.Fprintf(&b, "## 改动的文件（%d 个）\n%s\n\n", len(files), strings.Join(files, "\n"))
		}
		text := []rune(string(diff))
		if len(text) > maxDiffRunes {
			text = append(text[:maxDiffRunes], []rune("\n…（diff 太长，后面截断了；以上面的文件列表为准）")...)
		}
		fmt.Fprintf(&b, "## 完整 diff\n```diff\n%s\n```\n", string(text))
	}
	var out struct {
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	}
	if err := e.askJSON(ctx, e.Reviewer, "reviewer", t, prompts.Reviewer, b.String(), &out); err != nil {
		return event.TaskReviewedPayload{}, err
	}
	return event.TaskReviewedPayload{Pass: out.Pass, Reason: strings.TrimSpace(out.Reason)}, nil
}

// workRunsSinceDispatch counts the work runs since the task was last
// dispatched by hand, so a retry gets a fresh set of review rounds.
func (e *Engine) workRunsSinceDispatch(ctx context.Context, taskID string) int {
	since, _ := e.store().LastDispatch(ctx, taskID)
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: taskID})
	n := 0
	for _, r := range runs {
		if r.Purpose == event.PurposeWork && r.CreatedAt >= since {
			n++
		}
	}
	return n
}

func diffFiles(diff string) []string {
	var out []string
	for _, line := range strings.Split(diff, "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git a/"); ok {
			if i := strings.Index(rest, " b/"); i >= 0 {
				out = append(out, rest[:i])
			}
		}
	}
	return out
}

// Approve is the effect handler for an approval the approver agent may
// decide (auto mode). It allows the action, or hands it to the user.
func (e *Engine) Approve(ctx context.Context, eff store.Effect) error {
	a, err := e.store().Approval(ctx, eff.Key)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if a.Status != "pending" || a.Review != "ai" {
		return nil
	}
	allow := ""
	for _, kind := range []string{"allow_once", "allow_always"} {
		for _, o := range a.Options {
			if o.Kind == kind && allow == "" {
				allow = o.OptionID
			}
		}
	}
	if !models.Usable(ctx, e.Approver.Provider) || allow == "" {
		return e.Nodes.Escalate(ctx, a.ID, "没有配置审批模型，交给你决定")
	}
	t, _ := e.store().Task(ctx, a.TaskID)
	r, _ := e.store().Run(ctx, a.RunID)
	in := fmt.Sprintf("## 任务\n%s\n\n%s\n\n## agent 收到的说明\n%s\n\n## 它想做的操作\n类别：%s\n%s\n\n%s\n",
		t.Title, t.Detail, r.Prompt, a.Category, a.Title, a.Detail)
	var out struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := e.askJSON(ctx, e.Approver, "approver", t, prompts.Approver, in, &out); err != nil {
		if eff.Attempts+1 >= approverGiveUp {
			return e.Nodes.Escalate(ctx, a.ID, "审批模型调用失败，交给你决定："+err.Error())
		}
		return err
	}
	reason := strings.TrimSpace(out.Reason)
	if out.Decision == "allow" {
		return e.Nodes.DecideAs(ctx, a.ID, allow, reason, event.ActorApprover)
	}
	return e.Nodes.Escalate(ctx, a.ID, firstNonEmpty(reason, "审批 agent 拿不准"))
}

// askJSON calls a role's model with one message and decodes the JSON object
// in its answer.
func (e *Engine) askJSON(ctx context.Context, m Model, role string, t store.Task, system, user string, out any) error {
	resp, err := m.Provider.Stream(ctx, provider.Request{
		Model: m.Name, System: system, Messages: []provider.Message{provider.Text(provider.RoleUser, user)},
	}, nil)
	if err != nil {
		return err
	}
	ev := event.New(event.UsageRecorded, role, t.OriginThreadID, event.UsagePayload{
		Provider: m.Provider.Name(), Model: provider.ModelOf(resp, m.Name), Role: role,
		InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens,
	})
	ev.TaskID = t.ID
	e.Log.Append(ctx, ev)
	text := resp.Message.PlainText()
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j < i {
		return fmt.Errorf("%s 没有返回 JSON：%q", role, truncate(text, 200))
	}
	if err := json.Unmarshal([]byte(text[i:j+1]), out); err != nil {
		return fmt.Errorf("%s 返回的 JSON 无法解析：%w", role, err)
	}
	return nil
}

func (e *Engine) doneReport(ctx context.Context, t store.Task, prURL string) string {
	work := e.lastRun(ctx, t.ID, event.PurposeWork)
	var b strings.Builder
	fmt.Fprintf(&b, "任务「%s」完成了。", t.Title)
	if prURL != "" {
		fmt.Fprintf(&b, "\n\nPR：%s（sous 不会合并，等你评审）", prURL)
	}
	if work.DiffStat != "" {
		fmt.Fprintf(&b, "\n\n改动：%s", work.DiffStat)
	}
	if note := firstNonEmpty(t.ReviewNote, work.Summary); note != "" {
		fmt.Fprintf(&b, "\n\n%s", truncate(note, 800))
	}
	return b.String()
}

// report posts a message about the task in the thread it came from, or in
// the main thread. The task.status event beside it sends the push.
func (e *Engine) report(ctx context.Context, t store.Task, text string) {
	e.post(ctx, t, text, false)
}

// reportNotify is report for news no task.status push covers.
func (e *Engine) reportNotify(ctx context.Context, t store.Task, text string) {
	e.post(ctx, t, text, true)
}

func (e *Engine) post(ctx context.Context, t store.Task, text string, notify bool) {
	thread := t.OriginThreadID
	if thread == "" {
		thread, _ = e.store().MainThreadID(ctx)
	}
	ev := event.New(event.MessageAssistant, event.ActorSupervisor, thread,
		event.MessagePayload{MessageID: ids.New(), Content: text, Kind: event.KindReport, Notify: notify})
	ev.TaskID = t.ID
	if _, err := e.Log.Append(ctx, ev); err != nil {
		slog.Error("task: post report", "task", t.ID, "err", err)
	}
}

// cleanup removes a closed code task's worktrees, best effort: its branch
// stays, and a machine that is offline keeps its worktree.
func (e *Engine) cleanup(ctx context.Context, t store.Task) {
	if t.Kind != event.TaskKindCode {
		return
	}
	runs, _ := e.store().Runs(ctx, store.RunFilter{TaskID: t.ID})
	done := map[string]bool{}
	for _, r := range runs {
		if r.RepoPath == "" || done[r.NodeID] || r.Status == event.RunStatusQueued {
			continue
		}
		done[r.NodeID] = true
		if err := e.Nodes.RemoveWorkspace(r.NodeID, t.ID, r.RepoPath); err != nil {
			slog.Info("task: worktree kept", "task", t.ID, "node", r.NodeID, "reason", err)
		}
	}
}

func parseAgents(raw json.RawMessage) []proto.AgentInfo {
	var out []proto.AgentInfo
	json.Unmarshal(raw, &out)
	return out
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// cleanupWindow is how far back a reconnecting machine is asked to remove
// the worktrees of closed tasks it missed while offline.
const cleanupWindow = 30 * 24 * time.Hour

// cleanupNode asks a machine that just came online to remove the worktrees
// of code tasks that closed recently; removing a missing one is a no-op.
func (e *Engine) cleanupNode(ctx context.Context, nodeID string) {
	runs, err := e.store().Runs(ctx, store.RunFilter{NodeID: nodeID, Limit: 5000})
	if err != nil {
		return
	}
	since := time.Now().Add(-cleanupWindow).UnixMilli()
	done := map[string]bool{}
	for _, r := range runs {
		if r.RepoPath == "" || r.TaskID == "" || r.Kind == event.RunKindOps || done[r.TaskID] {
			continue
		}
		done[r.TaskID] = true
		t, err := e.store().Task(ctx, r.TaskID)
		if err != nil || !final(t.Status) || t.Kind != event.TaskKindCode || t.UpdatedAt < since {
			continue
		}
		e.Nodes.RemoveWorkspace(nodeID, t.ID, r.RepoPath)
	}
}

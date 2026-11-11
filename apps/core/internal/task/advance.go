package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/models"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
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

// Advance is the effect handler for a run that ended: a run of a task, or a
// git operation. Its payload is the run id.
func (e *Engine) Advance(ctx context.Context, eff store.Effect) error {
	r, err := e.store().Run(ctx, string(eff.Payload))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if r.Purpose == event.PurposeGit {
		return e.afterGit(ctx, r)
	}
	if r.Purpose == event.PurposeConsult || r.TaskID == "" {
		return nil
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
	if r.Purpose == event.PurposeChat {
		// Decision 0028: a direct chat has its own ending; it is not the
		// task's work run and must not be compared with one.
		return e.afterChat(ctx, r)
	}
	if last := e.lastRun(ctx, t.ID, event.PurposeWork, event.PurposeRebase, event.PurposeDeliver, event.PurposeReview); last.ID != r.ID {
		return nil // an older run; a newer one decides the task's state
	}

	if r.Status == event.RunStatusInterrupted && r.Purpose == event.PurposeWork {
		if r.Outcome == proto.OutcomeNeedsConfirmation {
			// Evidence already exists. Do not repeat completed deployments or
			// other external effects just because the ACP connection was lost.
			return e.confirmWork(ctx, t, r, "连接中断，工作可能已完成，等你验收")
		}
		if ok, err := e.recover(ctx, t, r); ok || err != nil {
			return err
		}
	}
	if r.Purpose == event.PurposeReview {
		return e.afterReviewRun(ctx, t, r)
	}
	if r.Purpose == event.PurposeDeliver || r.Purpose == event.PurposeRebase {
		// A delivery left over from before decision 0022: the work itself
		// was done; what to do with the branch is now up to the user.
		return e.finish(ctx, t, e.lastRun(ctx, t.ID, event.PurposeWork), "")
	}
	switch r.Status {
	case event.RunStatusSucceeded:
		if r.Outcome == proto.OutcomeNoAction && r.Purpose == event.PurposeWork {
			return e.confirmWork(ctx, t, r, "本轮正常结束，未记录到工具调用或本轮文件改动，等你验收")
		}
		if len(r.Refused) > 0 && r.Outcome != proto.OutcomeWorkProduced && r.Purpose == event.PurposeWork {
			// Refusal + no diff proves neither failure nor completion (ops
			// may have no repository diff). Only the Node's per-run evidence
			// bypasses confirmation; a normal end alone is not acceptance.
			return e.confirmWork(ctx, t, r, "本轮正常结束，有操作被拒绝，产出尚待确认，等你验收")
		}
		if t.Kind == event.TaskKindOps && t.DeployCommit != "" && r.Purpose == event.PurposeWork {
			return e.checkDeploy(ctx, t, r)
		}
		switch t.Review {
		case event.ReviewModel:
			if t.Status != event.TaskReview {
				if err := e.transition(ctx, t, event.TaskReview, "等待验收", "", event.ActorSystem); err != nil {
					return err
				}
			}
			e.mu.Unlock() // the reviewer may take a while; do not hold up dispatching
			locked = false
			return e.review(ctx, t.ID, r)
		case event.ReviewAgent:
			e.checkOverlap(ctx, t, r)
			return e.queueNext(ctx, t, event.PurposeReview)
		case event.ReviewUser:
			e.checkOverlap(ctx, t, r)
			return e.askUserReview(ctx, t, r, "")
		default:
			e.checkOverlap(ctx, t, r)
			return e.finish(ctx, t, r, "")
		}
	case event.RunStatusCancelled:
		return e.transition(ctx, t, event.TaskPlanned, "执行被取消："+firstNonEmpty(r.Error, "已取消"), "", event.ActorSystem)
	default:
		reason := firstNonEmpty(r.Error, r.Summary, "agent 没有完成")
		if text := ErrorKindText(r.ErrorKind, r.Agent); text != "" {
			reason = text
		}
		if r.Status == event.RunStatusInterrupted {
			reason = "执行被中断：" + reason
		}
		reason = truncate(reason, 300)
		if err := e.transition(ctx, t, event.TaskFailed, reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, fmt.Sprintf("任务「%s」失败了：%s%s\n\n可以让我重试，或者换一个 agent、换一台机器再做。", t.Title, reason, refusedNote(r)))
		return nil
	}
}

// ErrorKindText says in plain words why an agent stopped (proto.ErrAgent*);
// the agent's own message stays on the run.
func ErrorKindText(kind, agent string) string {
	who := firstNonEmpty(agent, "agent")
	switch kind {
	case proto.ErrAgentQuota:
		return who + " 的额度用完了（套餐或用量上限），什么都没做。换一个 agent，或者等额度恢复后重试"
	case proto.ErrAgentRateLimit:
		return who + " 被限流了，什么都没做。稍后重试，或者换一个 agent"
	case proto.ErrAgentNotLoggedIn:
		return who + " 在这台机器上没有登录。在机器页登录后重试，或者换一个 agent"
	case proto.ErrAgentUnavailable:
		return who + " 的服务暂时不可用。稍后重试，或者换一个 agent"
	}
	return ""
}

// refusedNote lists what was refused in a run, for a report.
func refusedNote(r store.Run) string {
	if len(r.Refused) == 0 {
		return ""
	}
	return "\n\n这次执行中被拒绝的操作：" + strings.Join(r.Refused, "；")
}

// confirmWork waits for acceptance without calling an uncertain run failed,
// assuming it is done, or automatically repeating its external side effects.
func (e *Engine) confirmWork(ctx context.Context, t store.Task, r store.Run, reason string) error {
	if t.Status == event.TaskReview && t.StatusReason == reason {
		return nil
	}
	e.checkOverlap(ctx, t, r)
	return e.askUserReview(ctx, t, r, reason)
}

// finish closes a task whose work is done (and reviewed, if it was to be).
// Nothing is committed, merged or pushed: the changes stay on the task's
// branch, or in place, until the user asks (decision 0022).
func (e *Engine) finish(ctx context.Context, t store.Task, work store.Run, verdict string) error {
	reason := firstNonEmpty(verdict, "完成")
	switch {
	case t.Place == event.PlaceWorktree && t.Branch != "" && work.DiffStat != "":
		reason = fmt.Sprintf("改动在分支 %s 上，还没合并", t.Branch)
	case t.Place == event.PlaceInPlace && work.DiffStat != "":
		reason = "已就地修改，可以撤销"
	}
	if err := e.transition(ctx, t, event.TaskDone, reason, "", event.ActorSystem); err != nil {
		return err
	}
	if t.Place == event.PlaceWorktree && t.Branch != "" && work.DiffStat != "" && t.GitState != event.GitUnmerged {
		e.gitEvent(ctx, t, event.GitUnmerged, "", work.ID)
	}
	if t.Kind == event.TaskKindOps && t.DeployCommit != "" && t.ProjectID != "" {
		ev := event.New(event.ProjectDeployed, event.ActorSystem, "", event.ProjectDeployedPayload{ProjectID: t.ProjectID, Commit: t.DeployCommit})
		if _, err := e.Log.Append(ctx, ev); err != nil {
			slog.Error("task: record the deployed commit", "task", t.ID, "err", err)
		}
	}
	e.report(ctx, t, e.doneReport(ctx, t, work))
	return nil
}

// gitEvent records what became of a task's branch.
func (e *Engine) gitEvent(ctx context.Context, t store.Task, state, prURL, runID string) {
	ev := event.New(event.TaskGit, event.ActorSystem, t.OriginThreadID, event.TaskGitPayload{State: state, PRURL: prURL, RunID: runID})
	ev.TaskID = t.ID
	if _, err := e.Log.Append(ctx, ev); err != nil {
		slog.Error("task: record git state", "task", t.ID, "err", err)
	}
}

// askUserReview leaves the verdict to the user: the task waits in "needs
// you" until the user (on the page or through the supervisor) decides.
func (e *Engine) askUserReview(ctx context.Context, t store.Task, r store.Run, note string) error {
	reason := firstNonEmpty(note, "等你验收")
	if err := e.transition(ctx, t, event.TaskReview, reason, "", event.ActorSystem, func(p *event.TaskStatusPayload) { p.Notify = true }); err != nil {
		return err
	}
	text := fmt.Sprintf("任务「%s」做完了，等你验收：看一下改动，说“通过”或者要改什么。", t.Title)
	if note != "" {
		text = fmt.Sprintf("任务「%s」：%s。请确认结果，说“通过”或者要改什么。", t.Title, note)
	}
	text += refusedNote(r)
	if r.DiffStat != "" {
		text += "\n\n改动：" + r.DiffStat
	}
	if r.Summary != "" {
		text += "\n\n" + summaryText(r.Summary, 800)
	}
	e.report(ctx, t, text)
	return nil
}

// Verdict records the user's review of a task waiting for one.
func (e *Engine) Verdict(ctx context.Context, id string, pass bool, reason, actor string) (store.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return store.Task{}, err
	}
	if t.Status != event.TaskReview {
		return store.Task{}, fmt.Errorf("%w: 任务现在不在等验收", ErrConflict)
	}
	if !pass && strings.TrimSpace(reason) == "" {
		return store.Task{}, fmt.Errorf("%w: 不通过时要说明原因，agent 会照着改", ErrInvalid)
	}
	work := e.lastRun(ctx, t.ID, event.PurposeWork)
	if err := e.applyVerdict(ctx, t, work, event.TaskReviewedPayload{Pass: pass, Reason: firstNonEmpty(strings.TrimSpace(reason), "用户验收通过")}, actor, true); err != nil {
		return store.Task{}, err
	}
	return e.store().Task(ctx, id)
}

// RequestReview reviews a task again by the given means; for a task that
// is done it reopens the review (the user asks "run the tests for me").
func (e *Engine) RequestReview(ctx context.Context, id, by, actor string) (store.Task, error) {
	if err := validAttrs("", by, "", ""); err != nil || by == "" || by == event.ReviewNone {
		return store.Task{}, fmt.Errorf("%w: 验收方式只能是 model、agent 或 user", ErrInvalid)
	}
	t, err := e.store().Task(ctx, id)
	if err != nil {
		return store.Task{}, err
	}
	if active(t.Status) && t.Status != event.TaskReview {
		return store.Task{}, fmt.Errorf("%w: 任务还在执行", ErrConflict)
	}
	work := e.lastRun(ctx, t.ID, event.PurposeWork)
	if work.ID == "" || (work.Status != event.RunStatusSucceeded && !(work.Status == event.RunStatusInterrupted && work.Outcome == proto.OutcomeNeedsConfirmation)) {
		return store.Task{}, fmt.Errorf("%w: 任务还没有做完的结果可以验收", ErrConflict)
	}
	if t.GitState == event.GitMerged || t.GitState == event.GitDiscarded {
		return store.Task{}, fmt.Errorf("%w: 任务的分支已经%s，没有东西可以验收", ErrConflict, gitStateName[t.GitState])
	}
	review := by
	if _, err := e.Update(ctx, id, event.TaskFields{Review: &review}, actor); err != nil {
		return store.Task{}, err
	}
	if t, err = e.store().Task(ctx, id); err != nil {
		return store.Task{}, err
	}
	switch by {
	case event.ReviewModel:
		e.mu.Lock()
		err = e.transition(ctx, t, event.TaskReview, "等待验收", "", actor)
		e.mu.Unlock()
		if err == nil {
			go func() {
				if err := e.review(context.WithoutCancel(ctx), id, work); err != nil {
					slog.Warn("task: review on request", "task", id, "err", err)
				}
			}()
		}
	case event.ReviewAgent:
		e.mu.Lock()
		err = e.queueNext(ctx, t, event.PurposeReview)
		e.mu.Unlock()
	case event.ReviewUser:
		e.mu.Lock()
		err = e.askUserReview(ctx, t, work, "")
		e.mu.Unlock()
	}
	if err != nil {
		return store.Task{}, err
	}
	return e.store().Task(ctx, id)
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

// review asks the reviewer model whether the run did the task, then
// applies the verdict.
func (e *Engine) review(ctx context.Context, taskID string, r store.Run) error {
	t, err := e.store().Task(ctx, taskID)
	if err != nil {
		return err
	}
	e.checkOverlap(ctx, t, r)
	if !models.Usable(ctx, e.Reviewer.Provider) {
		// No reviewer model: the user decides instead of a silent pass.
		e.mu.Lock()
		defer e.mu.Unlock()
		if t, err = e.store().Task(ctx, taskID); err != nil || t.Status != event.TaskReview {
			return err
		}
		return e.askUserReview(ctx, t, r, "没有配置验收模型，等你验收")
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
	return e.applyVerdict(ctx, t, r, verdict, event.ActorReviewer, false)
}

// afterReviewRun reads the reviewing agent's verdict from its summary.
func (e *Engine) afterReviewRun(ctx context.Context, t store.Task, r store.Run) error {
	work := e.lastRun(ctx, t.ID, event.PurposeWork)
	switch r.Status {
	case event.RunStatusSucceeded:
	case event.RunStatusCancelled:
		return e.transition(ctx, t, event.TaskPlanned, "验收被取消："+firstNonEmpty(r.Error, "已取消"), "", event.ActorSystem)
	default:
		return e.reviewFallback(ctx, t, r, work, "验收 agent 没能完成验收（"+truncate(firstNonEmpty(r.Error, r.Summary, "原因不明"), 200)+"），等你验收")
	}
	verdict, ok := parseVerdict(r.Summary)
	if !ok {
		return e.reviewFallback(ctx, t, r, work, "验收 agent 没有给出明确结论，等你验收")
	}
	return e.applyVerdict(ctx, t, work, verdict, event.ActorReviewer, false)
}

// reviewFallback is called with e.mu held. Release it while asking the model,
// then recheck the task and latest run before applying the result.
func (e *Engine) reviewFallback(ctx context.Context, t store.Task, review, work store.Run, reason string) error {
	if work.ID == "" || !models.Usable(ctx, e.Reviewer.Provider) {
		return e.askUserReview(ctx, t, work, reason)
	}
	if t.Status != event.TaskReview {
		if err := e.transition(ctx, t, event.TaskReview, "等待验收", "", event.ActorSystem); err != nil {
			return err
		}
	}
	e.mu.Unlock()
	verdict, verdictErr := e.verdict(ctx, t, work)
	e.mu.Lock()
	current, err := e.store().Task(ctx, t.ID)
	if err != nil {
		return err
	}
	if current.Status != event.TaskReview || e.lastRun(ctx, t.ID, event.PurposeWork, event.PurposeRebase, event.PurposeDeliver, event.PurposeReview).ID != review.ID {
		return nil
	}
	if verdictErr == nil {
		return e.applyVerdict(ctx, current, work, verdict, event.ActorReviewer, false)
	}
	return e.askUserReview(ctx, current, work, reason)
}

// parseVerdict finds {"pass": …, "reason": …} in a reviewing agent's answer.
func parseVerdict(text string) (event.TaskReviewedPayload, bool) {
	var out struct {
		Pass   *bool  `json:"pass"`
		Reason string `json:"reason"`
	}
	for i := strings.LastIndex(text, "{"); i >= 0; i = strings.LastIndex(text[:i], "{") {
		j := strings.Index(text[i:], "}")
		if j < 0 {
			continue
		}
		if json.Unmarshal([]byte(text[i:i+j+1]), &out) == nil && out.Pass != nil {
			return event.TaskReviewedPayload{Pass: *out.Pass, Reason: strings.TrimSpace(out.Reason)}, true
		}
	}
	return event.TaskReviewedPayload{}, false
}

// applyVerdict records a verdict and moves the task on: done when it
// passed; otherwise rework or a question, as the task says (and never more
// than MaxWorkRuns rounds). byUser marks the user's own verdict: a failed
// one always means "change this", so it goes back to work.
func (e *Engine) applyVerdict(ctx context.Context, t store.Task, work store.Run, v event.TaskReviewedPayload, actor string, byUser bool) error {
	ev := event.New(event.TaskReviewed, actor, t.OriginThreadID, v)
	ev.TaskID, ev.RunID = t.ID, work.ID
	if _, err := e.Log.Append(ctx, ev); err != nil {
		return err
	}
	t.ReviewNote = v.Reason
	if v.Pass {
		return e.finish(ctx, t, work, v.Reason)
	}
	if e.workRunsSinceDispatch(ctx, t.ID) >= MaxWorkRuns {
		reason := fmt.Sprintf("验收 %d 次都没有通过：%s", MaxWorkRuns, v.Reason)
		if err := e.transition(ctx, t, event.TaskBlocked, reason, "", event.ActorSystem); err != nil {
			return err
		}
		e.report(ctx, t, fmt.Sprintf("任务「%s」受阻：%s\n\n需要你看一下改动，决定怎么继续。", t.Title, reason))
		return nil
	}
	if byUser || (t.OnFail != event.OnFailAsk && len(work.Refused) == 0) { // decision 0024: rework unless told to stop
		return e.queueNext(ctx, t, event.PurposeWork)
	}
	reason := "验收没有通过：" + v.Reason
	hint := ""
	if len(work.Refused) > 0 {
		// Something was refused in this round: doing it again would ask the
		// same thing again. What the refusal meant is for the user to say.
		reason += "（执行中有操作被拒绝，没有自动返工）"
		hint = "执行中被拒绝的操作：" + strings.Join(work.Refused, "；") + "。拒绝可能就是用户不想这么做，不要直接让它照原样返工；先问用户想怎么办。\n\n"
	}
	if err := e.transition(ctx, t, event.TaskBlocked, reason, "", event.ActorSystem); err != nil {
		return err
	}
	e.tell(ctx, t, "review-failed:"+work.ID, "任务验收没有通过",
		fmt.Sprintf("任务 %s「%s」验收没有通过：%s\n\n"+hint+"判断怎么继续：让它照着意见返工（task_dispatch），换一个 agent 或岗位，或者问用户。"+
			"需要用户决定时用 notify 简短提醒一次；能自己推进就直接做。（这是 sous 的自动提醒，不是用户发的消息。）", t.ID, t.Title, v.Reason))
	return nil
}

// verdict runs the reviewer model on the task and the run's full diff.
func (e *Engine) verdict(ctx context.Context, t store.Task, r store.Run) (event.TaskReviewedPayload, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "## 任务\n%s\n\n%s\n\n", t.Title, t.Detail)
	fmt.Fprintf(&b, "## 交给 agent 的说明\n%s\n\n", r.Prompt)
	fmt.Fprintf(&b, "## agent 的总结\n%s\n\n", firstNonEmpty(r.Summary, "（没有总结）"))
	if len(r.Refused) > 0 {
		fmt.Fprintf(&b, "## 执行中被拒绝的操作\n%s\n\n（被拒绝的操作是用户或安全规则不允许的；agent 因此没做的部分，不要当成偷懒。）\n\n", strings.Join(r.Refused, "\n"))
	}
	if t.Place == event.PlaceWorktree || t.Place == event.PlaceInPlace {
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

// askJSON calls a role's model with one message and decodes the JSON object
// in its answer.
func (e *Engine) askJSON(ctx context.Context, m Model, role string, t store.Task, system, user string, out any) error {
	ctx = sysmap.WithRefs(ctx, sysmap.Refs{TaskID: t.ID})
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

func (e *Engine) doneReport(ctx context.Context, t store.Task, work store.Run) string {
	var b strings.Builder
	if t.Kind == event.TaskKindOps && t.Place == event.PlaceInPlace {
		fmt.Fprintf(&b, "运维任务「%s」完成了。", t.Title)
	} else {
		fmt.Fprintf(&b, "任务「%s」完成了。", t.Title)
	}
	switch {
	case t.Place == event.PlaceWorktree && t.Branch != "" && work.DiffStat != "":
		fmt.Fprintf(&b, "改动在分支 %s 上，没有合并也没有推送；要提交、合并、推送或开 PR 时告诉我。", t.Branch)
	case t.Place == event.PlaceInPlace && work.DiffStat != "":
		b.WriteString("是在项目目录里直接改的，开始前拍了快照，需要的话可以撤销。")
	}
	if work.DiffStat != "" {
		fmt.Fprintf(&b, "\n\n改动：%s", work.DiffStat)
	}
	if t.Kind == event.TaskKindOps && t.DeployCommit != "" {
		if proj, err := e.store().Project(ctx, t.ProjectID); err == nil && (proj.Deploy == nil || proj.Deploy.Health == "") {
			b.WriteString("\n\n项目没有配置健康检查，部署结果没有经过验证；可以在项目设置里加一条健康检查命令。")
		}
	}
	if t.ReviewNote != "" {
		fmt.Fprintf(&b, "\n\n验收意见：%s", truncate(t.ReviewNote, 400))
	}
	if work.Summary != "" {
		fmt.Fprintf(&b, "\n\n%s", summaryText(work.Summary, 800))
	} else if t.Kind == event.TaskKindOps {
		b.WriteString("\n\nagent 没有写总结")
	}
	b.WriteString(refusedNote(work))
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
	msgID := ids.New()
	ev := event.New(event.MessageAssistant, event.ActorSupervisor, thread,
		event.MessagePayload{MessageID: msgID, Content: text, Kind: event.KindReport, Notify: notify,
			TopicID: t.TopicID, TopicTitle: t.Title})
	ev.TaskID = t.ID
	evs := []event.Event{ev}
	if t.TopicID == "" && t.ID != "" {
		// A task without a topic (made on the workbench, or by the engine)
		// gets the one its first report starts (chat-topics).
		topic := msgID
		up := event.New(event.TaskUpdated, event.ActorSystem, thread, event.TaskUpdatedPayload{TopicID: &topic})
		up.TaskID = t.ID
		evs = append(evs, up)
		t.TopicID = topic
	}
	if _, err := e.Log.Append(ctx, evs...); err != nil {
		slog.Error("task: post report", "task", t.ID, "err", err)
		return
	}
	// The supervisor only wakes for input; a report is news it should act
	// on, so it gets a trigger pointing at it (decision 0025).
	t.OriginThreadID = thread
	what := "一个任务有新的汇报，在上面。"
	if t.ID != "" {
		if cur, err := e.store().Task(ctx, t.ID); err == nil {
			what = fmt.Sprintf("任务「%s」（id %s）现在%s，汇报在上面。", cur.Title, cur.ID, statusName[cur.Status])
		}
	}
	e.wake(ctx, t, "report:"+msgID, "任务有新进展", "【任务进展】"+what)
}

// worktreeDone reports whether nothing needs a task's worktree any more:
// its branch was merged, discarded or sent off as a pull request, or the
// task was cancelled. A done task with an unmerged branch keeps it, so the
// user can still look, rework or merge (decision 0022).
func worktreeDone(t store.Task) bool {
	switch t.GitState {
	case event.GitMerged, event.GitDiscarded, event.GitPR:
		return true
	}
	return t.Status == event.TaskCancelled
}

// cleanup removes a task's worktrees, best effort: its branch stays, and a
// machine that is offline keeps its worktree.
func (e *Engine) cleanup(ctx context.Context, t store.Task) {
	if t.Place != event.PlaceWorktree {
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
		if err != nil || t.Place != event.PlaceWorktree || t.UpdatedAt < since || !worktreeDone(t) {
			continue
		}
		e.Nodes.RemoveWorkspace(nodeID, t.ID, r.RepoPath)
	}
}

// reviewPrompt asks a reviewing agent to check a task's work in its
// directory: read only plus safe commands such as tests, and a verdict in
// JSON at the end.
func (e *Engine) reviewPrompt(ctx context.Context, t store.Task) string {
	work := e.lastRun(ctx, t.ID, event.PurposeWork)
	var b strings.Builder
	if p := e.reviewerPersona(ctx); strings.TrimSpace(p.Brief) != "" {
		fmt.Fprintf(&b, "【%s】%s\n\n", p.Name, strings.TrimSpace(p.Brief))
	}
	fmt.Fprintf(&b, "请验收这个任务做得对不对：\n\n任务：%s\n", t.Title)
	if t.Detail != "" {
		b.WriteString("\n" + t.Detail + "\n")
	}
	if work.Summary != "" {
		fmt.Fprintf(&b, "\n做这个任务的 agent 的总结：\n%s\n", truncate(work.Summary, 2000))
	}
	if work.DiffStat != "" {
		fmt.Fprintf(&b, "\n改动统计：%s\n", work.DiffStat)
	}
	switch t.Place {
	case event.PlaceWorktree:
		fmt.Fprintf(&b, "\n当前目录是任务分支 %s 的 worktree；用 git diff %s 看全部改动。", t.Branch, firstNonEmpty(t.BaseCommit, t.BaseBranch))
	case event.PlaceInPlace:
		b.WriteString("\n当前目录是项目的目录，任务直接在这里改过。")
	}
	b.WriteString("\n你只能读文件和运行测试、构建这类安全命令，不能改任何东西。按任务说明和完成标准检查：该跑的测试跑一遍，看改动是不是做了要求的事、有没有明显的问题。\n" +
		"最后单独一行输出结论 JSON：{\"pass\": true 或 false, \"reason\": \"一两句话，不通过时写清楚要改什么\"}")
	return b.String()
}

// checkDeploy follows a deploy's agent run with the project's health check,
// run by the node itself; only a passing check makes the deploy done and
// records the commit as deployed. Without a health check the deploy is done
// unverified, and the report says so.
func (e *Engine) checkDeploy(ctx context.Context, t store.Task, work store.Run) error {
	proj, err := e.store().Project(ctx, t.ProjectID)
	if err != nil || proj.Deploy == nil || strings.TrimSpace(proj.Deploy.Health) == "" {
		return e.finish(ctx, t, work, "")
	}
	thread := t.OriginThreadID
	if thread == "" {
		thread, _ = e.store().MainThreadID(ctx)
	}
	cr := nodehub.CreateRun{
		ThreadID: thread, Purpose: event.PurposeGit, TaskID: t.ID, NodeID: work.NodeID, RepoPath: work.RepoPath,
		Title: t.Title, Agent: "sous",
		PermissionMode: firstNonEmpty(t.PermissionMode, proj.DefaultMode, e.setting(ctx, SettingDefaultMode), proto.ModeAuto),
		Git:            &proto.GitOp{Op: proto.GitHealth, Command: strings.TrimSpace(proj.Deploy.Health)},
	}
	if _, err := e.Nodes.Create(ctx, cr); err != nil {
		return e.healthFailed(ctx, t, "健康检查没能开始："+err.Error())
	}
	return e.transition(ctx, t, event.TaskRunning, "部署命令执行完了，正在做健康检查", "", event.ActorSystem, waiting("", "health"))
}

// afterHealth finishes a deploy by its health check.
func (e *Engine) afterHealth(ctx context.Context, r store.Run) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store().Task(ctx, r.TaskID)
	if err != nil || final(t.Status) {
		return nil
	}
	if r.Status == event.RunStatusSucceeded {
		msg := "部署完成"
		if r.GitResult != nil && r.GitResult.Message != "" {
			msg += "，" + r.GitResult.Message
		}
		return e.finish(ctx, t, e.lastRun(ctx, t.ID, event.PurposeWork), truncate(msg, 300))
	}
	why := firstNonEmpty(r.Error, r.Summary, "原因不明")
	if r.GitResult != nil && r.GitResult.Message != "" {
		why = r.GitResult.Message
	}
	return e.healthFailed(ctx, t, why)
}

func (e *Engine) healthFailed(ctx context.Context, t store.Task, why string) error {
	reason := truncate("部署后的健康检查没有通过："+why, 300)
	if err := e.transition(ctx, t, event.TaskBlocked, reason, "", event.ActorSystem); err != nil {
		return err
	}
	e.report(ctx, t, fmt.Sprintf("部署任务「%s」没有完成：%s\n\n这个提交没有记成已部署。可以看一下服务器上的情况，修好后让我重试这个任务。", t.Title, why))
	return nil
}

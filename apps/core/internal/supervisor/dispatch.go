package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// This is a code backstop, not a prompt-only promise. IDs are checked even
// when the model uses an unfamiliar verb. State claims are checked per task,
// on every step, before any text is published.
var taskIDPattern = regexp.MustCompile(`(?i)\b[0-7][0-9A-HJKMNP-TV-Z]{25}\b`)
var dispatchClaim = regexp.MustCompile(`(?i)(?:已(?:经)?|刚刚|刚才)[^。！？\n，,；;]{0,24}(?:派发|派给|派出|安排|启动|开工|出[:：])|派好了|派完了|(?:任务|task)[^。！？\n，,；;]{0,24}(?:已交给|已经交给)|正在跑|正在执行|开工|\b(?:dispatched|started|running)\b`)
var completionClaim = regexp.MustCompile(`(?i)已(?:经)?完成|完成了|任务[^。！？\n，,；;]{0,32}完成|\b(?:completed|done|finished)\b`)
var dispatchQuote = regexp.MustCompile("“[^”]*”|「[^」]*」|\"[^\"]*\"|`[^`]*`")
var claimClause = regexp.MustCompile(`[。！？\n，,；;]`)
var claimSentence = regexp.MustCompile(`[^。！？\n]+[。！？\n]*|[。！？\n]+`)
var actualNote = regexp.MustCompile(`（实际：(?:任务|Run) [0-7][0-9A-HJKMNP-TV-Z]{25}：[^）]*）`)

func positiveClaim(text string, pattern *regexp.Regexp) bool {
	text = dispatchQuote.ReplaceAllString(text, "")
	for _, clause := range claimClause.Split(text, -1) {
		for _, match := range pattern.FindAllStringIndex(clause, -1) {
			// Negation qualifies this occurrence, not every other claim in the
			// sentence ("已派发，但尚未完成" still asserts dispatch).
			prefix := []rune(clause[:match[0]])
			if len(prefix) > 8 {
				prefix = prefix[len(prefix)-8:]
			}
			qualified := strings.ToLower(string(prefix) + clause[match[0]:match[1]])
			negated := false
			for _, word := range []string{"未", "没", "准备", "打算", "计划", "不能", "无法", "失败", "并非", "不是", "是否", "如果", "若", "not ", "never ", "hasn't", "haven't"} {
				negated = negated || strings.Contains(qualified, word)
			}
			if !negated {
				return true
			}
		}
	}

	return false
}

func claimsDispatch(text string) bool { return positiveClaim(text, dispatchClaim) }

const dispatchCheckNote = "【sous 派发核验】没有可核验的任务 id，不能声称已派发。请核对 task_list 或工具回执，如实说明任务 id 和当前状态；已存在的任务不要重复创建。排队不等于 agent 已启动。"
const dispatchUnconfirmed = "未能确认任务已派出（没有可核验的任务 id）。请查看任务列表中的实际状态。"

// A receipt records only IDs actually returned, never tool arguments or a
// turn-wide success flag. The database is always re-read for current state.
type taskReceipt struct{ Status, Reason string }
type claimVerifier struct {
	s        *Supervisor
	receipts map[string]taskReceipt
	threadID string
	replyTo  int64
	step     int
}

func (v *claimVerifier) collect(ctx context.Context, r provider.ToolResult) {
	if r.IsError {
		return
	} // errors often echo an invalid ID from the arguments
	for _, id := range taskIDPattern.FindAllString(r.Content, -1) {
		id = strings.ToUpper(id)
		receipt := taskReceipt{}
		if t, err := v.s.opt.Log.Store.Task(ctx, id); err == nil {
			receipt = taskReceipt{Status: t.Status, Reason: t.StatusReason}
		}
		v.receipts[id] = receipt
	}
}

func (v *claimVerifier) check(ctx context.Context, text string) string {
	// Rebuild our own annotations, so checking twice cannot add duplicates.
	text = actualNote.ReplaceAllString(text, "")
	return claimSentence.ReplaceAllStringFunc(text, func(sentence string) string {
		ids := taskIDPattern.FindAllString(sentence, -1)
		dispatch, complete := claimsDispatch(sentence), positiveClaim(sentence, completionClaim)
		seen := map[string]bool{}
		var notes, missing []string
		for _, raw := range ids {
			id := strings.ToUpper(raw)
			if seen[id] {
				continue
			}
			seen[id] = true
			t, err := v.s.opt.Log.Store.Task(ctx, id)
			receipt, returned := v.receipts[id]
			verdict := "verified"
			// task_create also returns a Run ULID. Do not mislabel it as a
			// missing task, or use a queued Run as evidence of startup.
			if errors.Is(err, store.ErrNotFound) && returned {
				if run, runErr := v.s.opt.Log.Store.Run(ctx, id); runErr == nil {
					valid := true
					actual := "Run " + run.Status
					if dispatch {
						valid = run.Status == event.RunStatusRunning && run.StartedAt > 0 && !run.Paused
						if run.TaskID != "" {
							owner, ownerErr := v.s.opt.Log.Store.Task(ctx, run.TaskID)
							valid = valid && ownerErr == nil && owner.Status == event.TaskRunning
							actual += "；任务 " + taskActual(owner)
						}
						if run.Status == event.RunStatusQueued || run.StartedAt == 0 {
							actual += "；尚未收到客户端启动确认"
						}
						if run.Paused {
							actual += "；已暂停"
						}
					}
					if complete {
						valid = valid && run.Status == event.RunStatusSucceeded
					}
					if !valid {
						notes = append(notes, "（实际：Run "+id+"："+actual+"）")
						verdict = "run_unconfirmed"
					}
					if verdict != "verified" {
						slog.Warn("supervisor: unverified dispatch claim", "thread", v.threadID, "reply_to", v.replyTo, "step", v.step, "task_id", run.TaskID, "run_id", id, "verdict", verdict, "status", run.Status)
					}
					continue
				}
			}
			switch {
			case err != nil && !errors.Is(err, store.ErrNotFound):
				missing = append(missing, "未能确认（任务查询失败："+id+"）")
				verdict = "lookup_error"
			case err != nil && !returned:
				missing = append(missing, "未能确认（未找到该任务："+id+"）")
				verdict = "not_found"
			case dispatch || complete:
				if err != nil {
					t = store.Task{ID: id, Status: receipt.Status, StatusReason: receipt.Reason}
				}
				actual := taskActual(t)
				if dispatch {
					r, runErr := v.s.opt.Log.Store.LatestTaskExecution(ctx, id)
					if t.Status != event.TaskRunning || runErr != nil || r.Status != event.RunStatusRunning || r.StartedAt == 0 || r.Paused {
						switch {
						case runErr != nil && !errors.Is(runErr, store.ErrNotFound):
							actual += "；Run 查询失败，无法确认启动"
						case r.Paused:
							actual += "；Run 已暂停"
						case runErr == nil && r.Status == event.RunStatusQueued:
							actual += "；Run 排队中，尚未收到客户端启动确认"
						case runErr == nil && r.Status != event.RunStatusRunning:
							actual += "；Run " + r.Status
						case runErr != nil || r.StartedAt == 0:
							actual += "；尚未收到客户端启动确认"
						}
						notes = append(notes, "（实际：任务 "+id+"："+actual+"）")
						verdict = "dispatch_unconfirmed"
					}
				}
				if complete && t.Status != event.TaskDone {
					note := "（实际：任务 " + id + "：" + actual + "）"
					if len(notes) == 0 || notes[len(notes)-1] != note {
						notes = append(notes, note)
					}
					verdict = "completion_unconfirmed"
				}
			}
			// Keep the original log name, adding the exact ID and disposition.
			if verdict != "verified" {
				slog.Warn("supervisor: unverified dispatch claim", "thread", v.threadID, "reply_to", v.replyTo, "step", v.step, "task_id", id, "verdict", verdict, "status", t.Status)
			}
		}
		if len(missing) > 0 {
			return strings.Join(missing, "；") + "。"
		}
		if dispatch && len(ids) == 0 {
			return dispatchUnconfirmed
		}
		if complete && len(ids) == 0 && (strings.Contains(sentence, "任务") || strings.Contains(strings.ToLower(sentence), "task")) {
			return "未能确认任务已完成（没有可核验的任务 id）。"
		}
		return sentence + strings.Join(notes, "")
	})
}

func taskActual(t store.Task) string {
	names := map[string]string{"queued": "排队中", "blocked": "受阻", "planned": "已计划", "inbox": "待整理", "running": "进行中", "waiting_approval": "等待审批", "review": "等待验收", "done": "已完成", "failed": "失败", "cancelled": "已取消"}
	state := names[t.Status]
	if state == "" {
		state = "状态未能确认"
	}
	if t.StatusReason != "" {
		// Keep annotations one-line and delimiters unambiguous on repeat checks.
		reason := strings.NewReplacer("）", ")", "。", ".", "！", "!", "？", "?", "\n", " ").Replace(t.StatusReason)
		state += "，" + reason
	}
	return state
}

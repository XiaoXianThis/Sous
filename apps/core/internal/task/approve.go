package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/models"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Limits that keep the approver agent from deciding without end
// (docs/decisions/0019).
const (
	// maxAIAllowsPerRun is how many operations the approver may allow in one
	// run; past it the user is asked, so a run that keeps asking for more is
	// seen by a person.
	maxAIAllowsPerRun = 150
	// maxAIDeniesPerOp is how often the approver refuses the same operation
	// before the user is told the agent keeps asking.
	maxAIDeniesPerOp = 3
	// historyShown is how many earlier decisions the approver is shown.
	historyShown = 10
)

// aiNever are the categories the approver agent never decides, whatever
// the node sent: the node already routes them to the user, and this is the
// second lock on the same door.
var aiNever = map[string]bool{"irreversible": true, "floor": true}

// Approve is the effect handler for an approval the approver agent may
// decide (auto mode). It allows the action, refuses it with a reason the
// agent can act on, or hands it to the user. Every path that is not a
// clear, confident "allow" or "deny" ends with the user.
func (e *Engine) Approve(ctx context.Context, eff store.Effect) error {
	a, err := e.store().Approval(ctx, eff.Key)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if a.Status != "pending" {
		return nil
	}
	if a.Review != "ai" {
		return e.followUser(ctx, a)
	}
	allow, reject := optionOf(a, "allow_once", "allow_always"), optionOf(a, "reject_once", "reject_always")
	if allow == "" {
		return e.Nodes.Escalate(ctx, a.ID, "这个请求没有可以批准的选项，交给你决定")
	}
	if aiNever[a.Category] {
		return e.Nodes.Escalate(ctx, a.ID, "这类操作（"+a.Category+"）不交给审批 agent，请你决定")
	}

	history, err := e.store().TaskApprovals(ctx, a.TaskID)
	if err != nil {
		return err
	}
	// The user already answered this very operation in this task: follow it.
	if prev, ok := userPrecedent(history, a); ok {
		if kindOfAnswer(prev) == "allow" {
			return e.Nodes.DecideAs(ctx, a.ID, allow, "你之前在这个任务里批准过同样的操作", event.ActorApprover)
		}
		if reject != "" {
			return e.Nodes.DecideAs(ctx, a.ID, reject, "你之前在这个任务里拒绝过同样的操作", event.ActorApprover)
		}
	}
	allows, denies := 0, 0
	for _, h := range history {
		if h.DecidedBy != event.ActorApprover || h.Status != "decided" {
			continue
		}
		switch {
		case kindOfAnswer(h) == "allow" && h.RunID == a.RunID:
			allows++
		case kindOfAnswer(h) == "reject" && h.Title == a.Title && h.Category == a.Category:
			denies++
		}
	}
	if allows >= maxAIAllowsPerRun {
		return e.Nodes.Escalate(ctx, a.ID, fmt.Sprintf("这次执行里审批 agent 已经批准了 %d 个操作，后面的请你看一下", allows))
	}
	if denies >= maxAIDeniesPerOp {
		return e.Nodes.Escalate(ctx, a.ID, fmt.Sprintf("同一个操作已被审批 agent 拒绝 %d 次，agent 还在重试，请你决定", denies))
	}

	if !models.Usable(ctx, e.Approver.Provider) {
		return e.Nodes.Escalate(ctx, a.ID, "没有配置审批模型，交给你决定")
	}
	t, _ := e.store().Task(ctx, a.TaskID)
	r, _ := e.store().Run(ctx, a.RunID)
	in := approverInput(t, r, a, history)
	var out struct {
		Decision   string `json:"decision"`
		Confidence string `json:"confidence"`
		Reason     string `json:"reason"`
	}
	if err := e.askJSON(ctx, e.Approver, "approver", t, prompts.Approver, in, &out); err != nil {
		if eff.Attempts+1 >= approverGiveUp {
			return e.Nodes.Escalate(ctx, a.ID, "审批模型调用失败，交给你决定："+err.Error())
		}
		return err
	}
	reason := strings.TrimSpace(out.Reason)
	sure := out.Confidence != "low" && out.Confidence != "medium"
	switch out.Decision {
	case "allow":
		if !sure {
			return e.Nodes.Escalate(ctx, a.ID, "审批 agent 把握不够（"+out.Confidence+"）："+firstNonEmpty(reason, "没有说明理由"))
		}
		return e.Nodes.DecideAs(ctx, a.ID, allow, firstNonEmpty(reason, "审批 agent 批准"), event.ActorApprover)
	case "deny":
		if reject == "" || out.Confidence == "low" {
			return e.Nodes.Escalate(ctx, a.ID, "审批 agent 倾向拒绝："+firstNonEmpty(reason, "没有说明理由"))
		}
		return e.Nodes.DecideAs(ctx, a.ID, reject, firstNonEmpty(reason, "审批 agent 认为这个操作不合适"), event.ActorApprover)
	}
	return e.Nodes.Escalate(ctx, a.ID, firstNonEmpty(reason, "审批 agent 拿不准"))
}

// optionOf returns the id of the first option of the given kinds, in order.
func optionOf(a store.Approval, kinds ...string) string {
	for _, kind := range kinds {
		for _, o := range a.Options {
			if o.Kind == kind {
				return o.OptionID
			}
		}
	}
	return ""
}

// kindOfAnswer says whether a decided approval was allowed ("allow"),
// refused ("reject"), or neither ("").
func kindOfAnswer(a store.Approval) string {
	if a.Status != "decided" || a.OptionID == "" {
		return ""
	}
	for _, o := range a.Options {
		if o.OptionID == a.OptionID {
			switch o.Kind {
			case "allow_once", "allow_always":
				return "allow"
			case "reject_once", "reject_always":
				return "reject"
			}
		}
	}
	return ""
}

// userPrecedent finds the user's latest answer to the same operation in
// the same task: same category, title and detail.
func userPrecedent(history []store.Approval, a store.Approval) (store.Approval, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		if h.ID != a.ID && h.DecidedBy == event.ActorUser && kindOfAnswer(h) != "" &&
			h.Category == a.Category && h.Title == a.Title && h.Detail == a.Detail {
			return h, true
		}
	}
	return store.Approval{}, false
}

// approverInput is what the approver reads: the task, the operation, and
// how earlier operations of the task were decided. The operation's title,
// detail and the agent's instructions come from an agent and are data.
func approverInput(t store.Task, r store.Run, a store.Approval, history []store.Approval) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 任务\n标题：%s\n类型：%s\n", t.Title, t.Kind)
	if t.Detail != "" {
		fmt.Fprintf(&b, "说明：%s\n", truncate(t.Detail, 3000))
	}
	if r.Prompt != "" {
		fmt.Fprintf(&b, "\n## agent 收到的说明\n%s\n", truncate(r.Prompt, 3000))
	}
	fmt.Fprintf(&b, "\n## 它想做的操作\n类别：%s\n标题：%s\n%s\n", a.Category, a.Title, truncate(a.Detail, 4000))

	var recent []string
	for i := len(history) - 1; i >= 0 && len(recent) < historyShown; i-- {
		h := history[i]
		if h.ID == a.ID {
			continue
		}
		var what string
		switch {
		case kindOfAnswer(h) == "allow":
			what = "批准"
		case kindOfAnswer(h) == "reject":
			what = "拒绝"
		case h.Status == "pending" && h.Review == "user" && h.Escalation != "":
			what = "转给用户，待定"
		default:
			continue
		}
		by := "用户"
		if h.DecidedBy == event.ActorApprover {
			by = "审批 agent"
		}
		line := fmt.Sprintf("- %s（%s）[%s] %s", what, by, h.Category, truncate(h.Title, 160))
		if h.Note != "" {
			line += " —— " + truncate(h.Note, 160)
		}
		recent = append(recent, line)
	}
	if len(recent) > 0 {
		b.WriteString("\n## 这个任务里最近的审批（新的在前）\n" + strings.Join(recent, "\n") + "\n")
	}
	return b.String()
}

// followUser answers an approval that waits for the user the way the user
// already answered the very same operation in this task, so a retried run
// does not ask again what was refused (report 4.2).
func (e *Engine) followUser(ctx context.Context, a store.Approval) error {
	if a.TaskID == "" {
		return nil
	}
	history, err := e.store().TaskApprovals(ctx, a.TaskID)
	if err != nil {
		return err
	}
	prev, ok := userPrecedent(history, a)
	if !ok {
		return nil
	}
	if kindOfAnswer(prev) == "allow" {
		// Only a refusal is followed without asking: an allowance given once
		// is the user's to repeat (the agent may have changed meanwhile).
		return nil
	}
	reject := optionOf(a, "reject_once", "reject_always")
	if reject == "" {
		return nil
	}
	return e.Nodes.DecideAs(ctx, a.ID, reject, "你之前在这个任务里拒绝过同样的操作", event.ActorApprover)
}

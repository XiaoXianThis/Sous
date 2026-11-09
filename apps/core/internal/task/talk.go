package task

import (
	"context"
	"net/http"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// The conversation between the supervisor and a task's agents
// (work/specs/chat-topics §5).

// Talk is one thing said between the supervisor (or the user) and a task's
// agents. From and To are supervisor, user, agent, reviewer or sous.
type Talk struct {
	Seq   int64  `json:"seq"`
	TS    int64  `json:"ts"`
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"` // prompt / message / progress / ask / result / review / report
	Title string `json:"title"`
	Text  string `json:"text"`
	RunID string `json:"run_id,omitempty"`
	Agent string `json:"agent,omitempty"`
}

// TalkLimit bounds the events read for one task.
const TalkLimit = 1000

var purposeTitle = map[string]string{
	event.PurposeWork: "派发", event.PurposeRebase: "变基", event.PurposeDeliver: "交付", event.PurposeReview: "验收",
}

// TaskTalk lists what the supervisor and a task's agents said to each other.
func (e *Engine) TaskTalk(ctx context.Context, taskID string) ([]Talk, error) {
	evs, err := e.store().TaskTalk(ctx, taskID, TalkLimit)
	if err != nil {
		return nil, err
	}
	type runInfo struct{ agent, purpose string }
	runs := map[string]runInfo{}
	works := 0
	out := []Talk{}
	add := func(ev event.Event, from, to, kind, title, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		out = append(out, Talk{Seq: ev.Seq, TS: ev.TS, From: from, To: to, Kind: kind, Title: title, Text: text,
			RunID: ev.RunID, Agent: runs[ev.RunID].agent})
	}
	for _, ev := range evs {
		switch ev.Type {
		case event.RunQueued:
			var p event.RunQueuedPayload
			if ev.Decode(&p) != nil || p.Purpose == event.PurposeGit || p.Purpose == event.PurposeConsult {
				continue
			}
			runs[ev.RunID] = runInfo{agent: p.Agent, purpose: p.Purpose}
			title := purposeTitle[p.Purpose]
			if title == "" {
				title = "派发"
			}
			if p.Purpose == event.PurposeWork || p.Purpose == "" {
				if works++; works > 1 {
					title = "返工"
				}
			}
			text := p.Prompt
			if p.RecoveryNote != "" {
				text = p.RecoveryNote + "\n\n" + text
			}
			from := event.ActorSupervisor
			if p.Purpose == event.PurposeReview {
				from = "sous"
			}
			add(ev, from, "agent", "prompt", title, text)
		case event.RunMessage:
			var p event.RunMessagePayload
			if ev.Decode(&p) != nil {
				continue
			}
			from := event.ActorSupervisor
			if ev.Actor == event.ActorUser {
				from = event.ActorUser
			}
			add(ev, from, "agent", "message", "追加指令", p.Text)
		case event.RunUpdate:
			var p event.RunUpdatePayload
			if ev.Decode(&p) != nil {
				continue
			}
			add(ev, "agent", event.ActorSupervisor, "progress", "进度", strings.TrimPrefix(p.Text, "进度："))
		case event.TriggerFired:
			var p event.TriggerPayload
			if ev.Decode(&p) != nil || !strings.HasPrefix(p.Key, "ask:") {
				continue
			}
			add(ev, "agent", event.ActorSupervisor, "ask", "提问", firstNonEmpty(p.Quote, p.Content))
		case event.RunEnded:
			info, ok := runs[ev.RunID]
			if !ok {
				continue // a git run, or one this list does not know
			}
			var p event.RunEndedPayload
			if ev.Decode(&p) != nil {
				continue
			}
			title := "结果"
			to := event.ActorSupervisor
			if info.purpose == event.PurposeReview {
				title, to = "验收结论", "sous"
			}
			text := p.Summary
			if p.Status != event.RunStatusSucceeded {
				title += "（" + p.Status + "）"
				if p.Error != "" {
					text = strings.TrimSpace(text + "\n\n" + p.Error)
				}
			}
			add(ev, "agent", to, "result", title, text)
		case event.TaskReviewed:
			var p event.TaskReviewedPayload
			if ev.Decode(&p) != nil {
				continue
			}
			title := "验收不通过"
			if p.Pass {
				title = "验收通过"
			}
			from := ev.Actor
			if from == "" || from == event.ActorSystem {
				from = event.ActorReviewer
			}
			add(ev, from, event.ActorSupervisor, "review", title, firstNonEmpty(p.Reason, title))
		case event.MessageAssistant:
			var p event.MessagePayload
			if ev.Decode(&p) != nil || p.Kind != event.KindReport {
				continue
			}
			add(ev, "sous", event.ActorSupervisor, "report", "汇报", p.Content)
		}
	}
	return out, nil
}

func (e *Engine) taskTalk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := e.store().Task(r.Context(), id); err != nil {
		taskError(w, err)
		return
	}
	talk, err := e.TaskTalk(r.Context(), id)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"talk": talk})
}

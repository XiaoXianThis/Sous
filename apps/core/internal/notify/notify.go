package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
)

// Settings keys.
const (
	SettingKinds = "notify_kinds" // JSON {kind: bool}; a missing kind is on
	SettingQuiet = "notify_quiet" // JSON Quiet
)

// Quiet hours: only approvals are pushed between Start and End.
type Quiet struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"` // "22:00"
	End     string `json:"end"`   // "08:00"
	TZ      string `json:"tz"`    // IANA zone of the user's device, e.g. Asia/Shanghai
}

// In reports whether t falls inside the quiet hours.
func (q Quiet) In(t time.Time) bool {
	if !q.Enabled {
		return false
	}
	start, ok1 := minutes(q.Start)
	end, ok2 := minutes(q.End)
	if !ok1 || !ok2 || start == end {
		return false
	}
	if loc, err := time.LoadLocation(q.TZ); err == nil && q.TZ != "" {
		t = t.In(loc)
	}
	now := t.Hour()*60 + t.Minute()
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end // across midnight
}

func minutes(hhmm string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Notifier turns stored events into push notifications.
type Notifier struct {
	Log    *eventlog.Log
	Sender *Sender
	Now    func() time.Time
}

// Kinds returns which kinds of notification are on.
func Kinds(ctx context.Context, st *store.Store) map[string]bool {
	out := map[string]bool{}
	for _, k := range event.PushKinds {
		out[k] = true
	}
	if raw, err := st.Setting(ctx, SettingKinds); err == nil {
		var m map[string]bool
		if json.Unmarshal([]byte(raw), &m) == nil {
			for k, v := range m {
				if _, known := out[k]; known {
					out[k] = v
				}
			}
		}
	}
	return out
}

// QuietHours returns the configured quiet hours.
func QuietHours(ctx context.Context, st *store.Store) Quiet {
	q := Quiet{Start: "22:00", End: "08:00"}
	if raw, err := st.Setting(ctx, SettingQuiet); err == nil {
		json.Unmarshal([]byte(raw), &q)
	}
	return q
}

// Handle is the effect handler for EffectPush; the payload is the event.
func (n *Notifier) Handle(ctx context.Context, eff store.Effect) error {
	var ev event.Event
	if err := json.Unmarshal(eff.Payload, &ev); err != nil {
		return nil // nothing to retry
	}
	kind := event.PushKind(ev)
	if kind == "" {
		return nil
	}
	st := n.Log.Store
	if !Kinds(ctx, st)[kind] {
		return nil
	}
	now := time.Now()
	if n.Now != nil {
		now = n.Now()
	}
	if kind != event.PushApproval && QuietHours(ctx, st).In(now) {
		return nil
	}
	msg, ok := n.message(ctx, ev, kind)
	if !ok {
		return nil
	}
	subs, err := st.PushSubscriptions(ctx)
	if err != nil {
		return err
	}
	if len(subs) == 0 || n.Sender == nil {
		return nil
	}
	sent := 0
	for _, s := range subs {
		err := n.Sender.Send(ctx, Subscription{Endpoint: s.Endpoint, P256DH: s.P256DH, Auth: s.Auth}, msg, kind == event.PushApproval)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, ErrGone):
			st.DeletePushSubscription(ctx, s.Endpoint)
		default:
			slog.Warn("notify: push failed", "endpoint", shortEndpoint(s.Endpoint), "err", err)
		}
	}
	rec := event.New(event.NotifySent, event.ActorSystem, "", event.NotifySentPayload{
		Kind: kind, Title: msg.Title, Body: msg.Body, URL: msg.URL, Sent: sent,
	})
	rec.TaskID, rec.RunID = ev.TaskID, ev.RunID
	n.Log.Append(ctx, rec)
	return nil
}

// message words the notification. URLs are hash routes of the PWA.
func (n *Notifier) message(ctx context.Context, ev event.Event, kind string) (Message, bool) {
	st := n.Log.Store
	taskTitle := func() string {
		if t, err := st.Task(ctx, ev.TaskID); err == nil {
			return t.Title
		}
		return ""
	}
	switch ev.Type {
	case event.ApprovalRequested, event.ApprovalEscalated:
		var p struct {
			ApprovalID string `json:"approval_id"`
		}
		ev.Decode(&p)
		a, err := st.Approval(ctx, p.ApprovalID)
		if err != nil || a.Status != "pending" {
			return Message{}, false // already answered
		}
		body := a.Title
		if t := taskTitle(); t != "" {
			body = t + "：" + a.Title
		}
		if a.Escalation != "" {
			body += "（审批 agent：" + a.Escalation + "）"
		}
		return Message{Title: "需要你审批", Body: clip(body), URL: "/#approvals", Tag: "approval-" + a.ID, Kind: kind}, true
	case event.TaskStatus:
		var p event.TaskStatusPayload
		ev.Decode(&p)
		titles := map[string]string{
			event.PushTaskDone: "任务完成", event.PushTaskFailed: "任务失败", event.PushTaskBlocked: "任务受阻",
		}
		title := titles[kind]
		if p.To == event.TaskQueued {
			title = "任务在排队"
		}
		body := taskTitle()
		if p.Reason != "" {
			body += "：" + p.Reason
		}
		url, tag := "/#task="+ev.TaskID, "task-"+ev.TaskID
		if t, err := st.Task(ctx, ev.TaskID); err == nil && t.TopicID != "" {
			// One notification per topic: news of the same topic replaces it.
			url, tag = "/#topic="+t.TopicID, "topic-"+t.TopicID
		}
		return Message{Title: title, Body: clip(body), URL: url, Tag: tag, Kind: kind}, true
	case event.InspectorFinding:
		var p event.FindingPayload
		ev.Decode(&p)
		title := map[string]string{event.ActionPause: "巡检：已暂停一个 Run", event.ActionStop: "巡检：已终止一个 Run"}[p.Action]
		if title == "" || !p.Done {
			title = "巡检发现问题"
		}
		url := "/#thread=" + ev.ThreadID
		if ev.TaskID != "" {
			url = "/#task=" + ev.TaskID
		}
		return Message{Title: title, Body: clip(p.Reason), URL: url, Tag: "finding-" + p.FindingID, Kind: kind}, true
	case event.MessageAssistant:
		var p event.MessagePayload
		ev.Decode(&p)
		url, tag := "/#thread="+ev.ThreadID, "msg-"+p.MessageID
		if topic := p.TopicID; topic != "" || p.Kind == event.KindReport || p.Kind == event.KindNotice {
			if topic == "" {
				topic = p.MessageID // the message started its topic
			}
			url, tag = "/#topic="+topic, "topic-"+topic
		}
		return Message{Title: "sous", Body: clip(p.Content), URL: url, Tag: tag, Kind: kind}, true
	}
	return Message{}, false
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 180 {
		return string(r[:180]) + "…"
	}
	return s
}

func shortEndpoint(e string) string {
	if len(e) > 48 {
		return e[:48] + "…"
	}
	return e
}

// RegisterSystemProbes exposes push activity on the system map.
func RegisterSystemProbes() {
	sysmap.RegisterProbe("notify", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		n := sysmap.Sum("notify>push", 900)
		state := sysmap.Idle
		if n > 0 {
			state = sysmap.OK
		}
		return sysmap.Status{State: state, Summary: fmt.Sprintf("最近 15 分钟发了 %d 条", n)}
	}))
	sysmap.RegisterProbe("push", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		state := sysmap.Idle
		if sysmap.Sum("notify>push", 900) > 0 {
			state = sysmap.OK
		}
		return sysmap.Status{State: state, Summary: "浏览器推送服务"}
	}))
}

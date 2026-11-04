package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Options tunes the triggers; zero values take the defaults.
type Options struct {
	// Tick is how often schedules are checked (default 30s).
	Tick time.Duration
	// FollowupEvery is how often stuck work is looked for (default 5 minutes).
	FollowupEvery time.Duration
	// BlockedAfter: a blocked or failed task the user has not dismissed is
	// followed up after this long (default 4h).
	BlockedAfter time.Duration
	// RunTimeout: a run still going after this long is followed up (default 2h).
	RunTimeout time.Duration
	// ApprovalAfter: an approval the user has not answered (default 2h).
	ApprovalAfter time.Duration
	// QuestionAfter: a question the supervisor asked that got no answer (default 8h).
	QuestionAfter time.Duration
	// RetentionDays: logs and diffs of tasks closed longer ago are archived
	// (default 90; negative turns archiving off).
	RetentionDays int
	// Archive does the archiving (nodehub.Hub.Archive).
	Archive func(ctx context.Context, cutoff time.Time) (int, error)
	// Beat is called on every loop iteration, for the watchdog.
	Beat func()
	Now  func() time.Time
}

func (o *Options) defaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.Tick, 30*time.Second)
	def(&o.FollowupEvery, 5*time.Minute)
	def(&o.BlockedAfter, 4*time.Hour)
	def(&o.RunTimeout, 2*time.Hour)
	def(&o.ApprovalAfter, 2*time.Hour)
	def(&o.QuestionAfter, 8*time.Hour)
	if o.RetentionDays == 0 {
		o.RetentionDays = 90
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Beat == nil {
		o.Beat = func() {}
	}
}

type Scheduler struct {
	Log  *eventlog.Log
	opt  Options
	kick chan struct{}
}

func New(log *eventlog.Log, opt Options) *Scheduler {
	opt.defaults()
	return &Scheduler{Log: log, opt: opt, kick: make(chan struct{}, 1)}
}

func (s *Scheduler) store() *store.Store { return s.Log.Store }

// Kick makes the scheduler look at the schedules now.
func (s *Scheduler) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Start runs the loops until ctx ends.
func (s *Scheduler) Start(ctx context.Context) {
	s.Log.Listen(func(ev event.Event) {
		if ev.Type == event.SchedulePut {
			s.Kick()
		}
	})
	go func() {
		tick := time.NewTicker(s.opt.Tick)
		defer tick.Stop()
		follow := time.NewTicker(s.opt.FollowupEvery)
		defer follow.Stop()
		var lastArchive time.Time
		for {
			s.opt.Beat()
			s.RunSchedules(ctx)
			select {
			case <-ctx.Done():
				return
			case <-s.kick:
			case <-tick.C:
			case <-follow.C:
				s.Followups(ctx)
				if s.opt.RetentionDays > 0 && s.opt.Archive != nil && s.opt.Now().Sub(lastArchive) > 24*time.Hour {
					lastArchive = s.opt.Now()
					cutoff := s.opt.Now().AddDate(0, 0, -s.opt.RetentionDays)
					if _, err := s.opt.Archive(ctx, cutoff); err != nil {
						slog.Warn("trigger: archive old runs", "err", err)
					}
				}
			}
		}
	}()
}

// NextRun returns when a schedule fires next, after its last run (or its
// creation), or the zero time.
func NextRun(x store.Schedule) time.Time {
	c, err := ParseCron(x.Cron)
	if err != nil {
		return time.Time{}
	}
	// An edit restarts the clock, so changing the time never fires at once.
	from := max(x.LastRun, x.UpdatedAt)
	return c.Next(time.UnixMilli(from), location(x.TZ))
}

func location(tz string) *time.Location {
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	return time.Local
}

// RunSchedules fires every enabled schedule that is due. A schedule missed
// while the Hub was down fires once when it comes back.
func (s *Scheduler) RunSchedules(ctx context.Context) {
	all, err := s.store().Schedules(ctx)
	if err != nil {
		slog.Error("trigger: list schedules", "err", err)
		return
	}
	now := s.opt.Now()
	for _, x := range all {
		next := NextRun(x)
		if !x.Enabled || next.IsZero() || next.After(now) {
			continue
		}
		content := fmt.Sprintf("【定时任务「%s」】\n%s\n\n（这是用户设定的定时任务，现在到时间了。照着做，做完按平常的规则决定要不要发言。）", x.Title, x.Prompt)
		if err := s.fire(ctx, event.TriggerCron, "schedule:"+x.ID, x.ID, x.ThreadID, x.Title, content); err != nil {
			slog.Error("trigger: fire schedule", "schedule", x.ID, "err", err)
		}
	}
}

// fire appends a trigger.fired event: the supervisor reads it as a user
// message of its thread (the main thread when threadID is empty or gone).
func (s *Scheduler) fire(ctx context.Context, kind, key, scheduleID, threadID, title, content string) error {
	if threadID != "" {
		if t, err := s.store().Thread(ctx, threadID); err != nil || t.Archived {
			threadID = ""
		}
	}
	if threadID == "" {
		var err error
		if threadID, err = s.store().MainThreadID(ctx); err != nil {
			return err
		}
	}
	ev := event.New(event.TriggerFired, event.ActorTrigger, threadID, event.TriggerPayload{
		Kind: kind, Key: key, ScheduleID: scheduleID, MessageID: ids.New(), Title: title, Content: content, At: s.opt.Now().UnixMilli(),
	})
	_, err := s.Log.Append(ctx, ev)
	if err == nil {
		slog.Info("trigger: fired", "kind", kind, "key", key)
	}
	return err
}

// once fires a follow-up unless its key fired before.
func (s *Scheduler) once(ctx context.Context, key, threadID, title, content string) {
	if at, err := s.store().TriggerFiredAt(ctx, key); err != nil || at > 0 {
		return
	}
	if err := s.fire(ctx, event.TriggerFollowup, key, "", threadID, title, content); err != nil {
		slog.Error("trigger: fire follow-up", "key", key, "err", err)
	}
}

const followupTail = "\n\n（这是 sous 的自动跟进，不是用户发的消息。先判断能不能自己推进；需要用户决定或行动时，用 notify 简短提醒一次，一次只说一件事。什么都不需要做时调用 stay_silent。）"

// Followups looks for work that waits too long (requirements R7.2) and
// asks the supervisor to follow up, once per situation.
func (s *Scheduler) Followups(ctx context.Context) {
	st := s.store()
	now := s.opt.Now()
	hours := func(since int64) string {
		d := now.Sub(time.UnixMilli(since))
		if d < time.Hour {
			return fmt.Sprintf("%d 分钟", int(d.Minutes()))
		}
		return fmt.Sprintf("%.0f 小时", d.Hours())
	}

	// Questions first: a follow-up in the same thread becomes its last message.
	threads, _ := st.Threads(ctx)
	for _, th := range threads {
		msgs, err := st.Messages(ctx, th.ID, 0, 1)
		if err != nil || len(msgs) == 0 {
			continue
		}
		m := msgs[0]
		age := now.Sub(time.UnixMilli(m.CreatedAt))
		if m.Role != "assistant" || m.Kind != "" || m.Error || age < s.opt.QuestionAfter || age > 3*24*time.Hour || !asks(m.Content) {
			continue
		}
		s.once(ctx, "question:"+m.ID, th.ID, "问题没有回复",
			fmt.Sprintf("【跟进】你在这个对话里问用户的问题，已经 %s 没有回复了：\n%s\n\n如果这件事还卡在用户这里，用 notify 提醒一次；不重要就不用管。", hours(m.CreatedAt), tail(m.Content, 300))+followupTail)
	}

	stuck, _ := st.Tasks(ctx, store.TaskFilter{Statuses: []string{event.TaskBlocked, event.TaskFailed}})
	for _, t := range stuck {
		if t.Dismissed || now.Sub(time.UnixMilli(t.UpdatedAt)) < s.opt.BlockedAfter {
			continue
		}
		what := map[string]string{event.TaskBlocked: "受阻", event.TaskFailed: "失败"}[t.Status]
		s.once(ctx, fmt.Sprintf("task:%s:%d", t.ID, t.UpdatedAt), t.OriginThreadID, "跟进任务",
			fmt.Sprintf("【跟进】任务「%s」（id %s）已经%s %s，原因：%s。可以用 task_dispatch 重试或换 agent、换机器，也可以修改任务；需要用户拿主意时提醒用户。",
				t.Title, t.ID, what, hours(t.UpdatedAt), t.StatusReason)+followupTail)
	}

	running, _ := st.Runs(ctx, store.RunFilter{Statuses: []string{event.RunStatusRunning}})
	for _, r := range running {
		if r.Paused || r.StartedAt == 0 || now.Sub(time.UnixMilli(r.StartedAt)) < s.opt.RunTimeout {
			continue
		}
		thread := r.ThreadID
		subject := "一个 Run"
		if t, err := st.Task(ctx, r.TaskID); err == nil {
			subject = fmt.Sprintf("任务「%s」（id %s）", t.Title, t.ID)
			thread = t.OriginThreadID
		}
		s.once(ctx, "run:"+r.ID+":timeout", thread, "Run 超时",
			fmt.Sprintf("【跟进】%s的 Run（%s）已经执行了 %s，最近一次进展在 %s 前。判断它是不是卡住了：卡住了就用 run_cancel 停掉再决定是否重试；还在正常推进就不用管。",
				subject, r.Agent, hours(r.StartedAt), hours(r.LastEvent))+followupTail)
	}

	approvals, _ := st.Approvals(ctx, "pending", "")
	for _, a := range approvals {
		if a.Review != "user" || now.Sub(time.UnixMilli(a.CreatedAt)) < s.opt.ApprovalAfter {
			continue
		}
		s.once(ctx, "approval:"+a.ID, a.ThreadID, "审批在等",
			fmt.Sprintf("【跟进】有一个操作等用户审批已经 %s 了：%s。你不能替用户审批；用 notify 提醒用户一次就好。", hours(a.CreatedAt), a.Title)+followupTail)
	}

}

// asks reports whether a reply ends with a question to the user.
func asks(text string) bool {
	t := strings.TrimSpace(text)
	r := []rune(t)
	if len(r) > 40 {
		r = r[len(r)-40:]
	}
	return strings.ContainsAny(string(r), "?？")
}

func tail(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return string(r)
}

// DefaultTZ is the time zone new schedules use when none is given: the one
// the user's device reported for quiet hours, or the Hub's.
func DefaultTZ(ctx context.Context, st *store.Store) string {
	raw, err := st.Setting(ctx, "notify_quiet")
	if err == nil {
		var q struct {
			TZ string `json:"tz"`
		}
		if json.Unmarshal([]byte(raw), &q) == nil && q.TZ != "" {
			return q.TZ
		}
	}
	return ""
}

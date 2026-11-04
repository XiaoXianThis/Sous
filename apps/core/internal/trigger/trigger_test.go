package trigger

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestCronNext(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, sh)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct{ expr, from, want string }{
		{"0 9 * * *", "2026-11-04 08:59", "2026-11-04 09:00"},
		{"0 9 * * *", "2026-11-04 09:00", "2026-11-05 09:00"},
		{"*/15 * * * *", "2026-11-04 10:01", "2026-11-04 10:15"},
		{"30 18 * * 1-5", "2026-11-06 19:00", "2026-11-09 18:30"}, // Friday evening -> Monday
		{"0 0 1 * *", "2026-11-04 00:00", "2026-12-01 00:00"},
		{"0 8 * * 7", "2026-11-04 00:00", "2026-11-08 08:00"}, // 7 is Sunday
		{"@daily", "2026-12-31 23:59", "2027-01-01 00:00"},
		{"0 12 13 * 5", "2026-11-01 00:00", "2026-11-06 12:00"}, // either the 13th or a Friday
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"},
	}
	for _, c := range cases {
		cr, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := cr.Next(at(c.from), sh); !got.Equal(at(c.want)) {
			t.Errorf("%s from %s: got %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04 Mon"), c.want)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a b c d e", "5-1 * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

type env struct {
	t    *testing.T
	log  *eventlog.Log
	main string
	now  time.Time
	s    *Scheduler
}

func newEnv(t *testing.T) *env {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := eventlog.New(st, event.NewBus())
	main, _ := log.EnsureMainThread(context.Background())
	e := &env{t: t, log: log, main: main, now: time.Now()}
	e.s = New(log, Options{Now: func() time.Time { return e.now }})
	return e
}

func (e *env) triggers() []store.Message {
	msgs, _ := e.log.Store.Messages(context.Background(), e.main, 0, 100)
	var out []store.Message
	for _, m := range msgs {
		if m.Kind == event.KindTrigger {
			out = append(out, m)
		}
	}
	return out
}

func TestScheduleFiresOnceWhenDue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cron, prompt, tz := "*/5 * * * *", "汇总一下进展", "UTC"
	x, err := e.s.Put(ctx, "", ScheduleInput{Cron: &cron, Prompt: &prompt, TZ: &tz}, event.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if x.NextRun == 0 || x.Title == "" {
		t.Fatalf("schedule: %+v", x)
	}
	e.s.RunSchedules(ctx)
	if n := len(e.triggers()); n != 0 {
		t.Fatalf("fired before it was due: %d", n)
	}
	// The Hub was down for an hour: the schedule fires once, not twelve times.
	e.now = e.now.Add(time.Hour)
	e.s.RunSchedules(ctx)
	e.s.RunSchedules(ctx)
	got := e.triggers()
	if len(got) != 1 || got[0].Role != "user" || !strings.Contains(got[0].Content, "汇总一下进展") || !strings.Contains(got[0].Content, "定时任务") {
		t.Fatalf("triggers: %+v", got)
	}
	x, _ = e.log.Store.Schedule(ctx, x.ID)
	if x.LastRun == 0 {
		t.Fatal("last run not recorded")
	}
	// Disabled schedules never fire.
	off := false
	e.s.Put(ctx, x.ID, ScheduleInput{Enabled: &off}, event.ActorUser)
	e.now = e.now.Add(time.Hour)
	e.s.RunSchedules(ctx)
	if n := len(e.triggers()); n != 1 {
		t.Fatalf("a disabled schedule fired: %d", n)
	}
	bad := "every day"
	if _, err := e.s.Put(ctx, x.ID, ScheduleInput{Cron: &bad}, event.ActorUser); err == nil {
		t.Fatal("a bad cron was accepted")
	}
}

func TestFollowupsFireOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// A blocked task.
	tk := event.New(event.TaskCreated, event.ActorUser, e.main, event.TaskCreatedPayload{Kind: "general", Title: "修 bug", Status: event.TaskPlanned})
	tk.TaskID = ids.New()
	st := event.New(event.TaskStatus, event.ActorSystem, e.main, event.TaskStatusPayload{From: "planned", To: event.TaskBlocked, Reason: "验收 3 次都没有通过"})
	st.TaskID = tk.TaskID
	// A question the supervisor asked.
	q := event.New(event.MessageAssistant, event.ActorSupervisor, e.main, event.MessagePayload{MessageID: ids.New(), Content: "要放在哪个项目里？"})
	if _, err := e.log.Append(ctx, tk, st, q); err != nil {
		t.Fatal(err)
	}
	e.s.Followups(ctx)
	if n := len(e.triggers()); n != 0 {
		t.Fatalf("followed up too early: %d", n)
	}
	e.now = e.now.Add(9 * time.Hour)
	e.s.Followups(ctx)
	e.s.Followups(ctx)
	got := e.triggers()
	if len(got) != 2 {
		t.Fatalf("follow-ups: %+v", got)
	}
	all := got[0].Content + got[1].Content
	if !strings.Contains(all, "修 bug") || !strings.Contains(all, "受阻") || !strings.Contains(all, "要放在哪个项目里") || !strings.Contains(all, "stay_silent") {
		t.Fatalf("follow-ups: %s", all)
	}
}

func TestQuestionDetection(t *testing.T) {
	if !asks("你想用哪个 agent？") || !asks("Which one?") || asks("好的，已经派给 claude。") {
		t.Fatal("asks")
	}
}

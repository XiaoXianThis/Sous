// Package inspector is the inspector agent (docs/decisions/0006): every
// few minutes it takes a snapshot of the running runs and recent token use,
// lets cheap rules pick what looks wrong (a tool called over and over, no
// progress, running far too long, tokens burning fast), asks the inspector
// model about those, and pauses, stops or just reports them.
package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
)

// Rules.
const (
	RuleRepeat  = "repeat"
	RuleStalled = "stalled"
	RuleLong    = "long"
	RuleTokens  = "tokens"
)

// Runs is what the inspector needs from the node hub.
type Runs interface {
	Pause(ctx context.Context, runID, reason, by string) error
	Cancel(ctx context.Context, runID, reason string) error
}

// Options tunes the inspector; zero values take the defaults.
type Options struct {
	Every        time.Duration // default 10 minutes
	Repeat       int           // more identical tool calls in a row than this is a loop (default 5)
	StalledAfter time.Duration // no progress for this long (default 30 minutes)
	LongAfter    time.Duration // running for this long (default 3 hours)
	TokenAlarm   int           // model tokens in one period (default 2,000,000)
	Beat         func()
	Now          func() time.Time
}

func (o *Options) defaults() {
	if o.Every <= 0 {
		o.Every = 10 * time.Minute
	}
	if o.Repeat <= 0 {
		o.Repeat = 5
	}
	if o.StalledAfter <= 0 {
		o.StalledAfter = 30 * time.Minute
	}
	if o.LongAfter <= 0 {
		o.LongAfter = 3 * time.Hour
	}
	if o.TokenAlarm <= 0 {
		o.TokenAlarm = 2_000_000
	}
	if o.Beat == nil {
		o.Beat = func() {}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

type Inspector struct {
	mu        sync.Mutex
	lastRun   time.Time
	lastFound int
	Log       *eventlog.Log
	Runs      Runs
	Provider  provider.Provider // nil: the rules decide alone
	Model     string
	opt       Options
}

func New(log *eventlog.Log, runs Runs, p provider.Provider, model string, opt Options) *Inspector {
	opt.defaults()
	in := &Inspector{Log: log, Runs: runs, Provider: p, Model: model, opt: opt}
	sysmap.RegisterProbe("inspector", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		in.mu.Lock()
		defer in.mu.Unlock()
		if in.lastRun.IsZero() {
			return sysmap.Status{State: sysmap.Idle, Summary: "还没有巡检过"}
		}
		ago := sysmap.Ago(in.lastRun)
		if in.lastFound > 0 {
			return sysmap.Status{State: sysmap.Degraded, Summary: fmt.Sprintf("%s巡检，发现 %d 个问题", ago, in.lastFound)}
		}
		return sysmap.Status{State: sysmap.OK, Summary: fmt.Sprintf("%s巡检，没有问题", ago)}
	}))
	return in
}

// Start inspects every period until ctx ends.
func (in *Inspector) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(in.opt.Every)
		defer t.Stop()
		for {
			in.opt.Beat()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				in.Inspect(ctx)
			}
		}
	}()
}

// Suspect is a run (or the token use) a rule flagged.
type Suspect struct {
	Rule   string
	Run    store.Run
	Facts  string // what the rule saw, for the model and the user
	Action string // what to do without a model
	Sev    string
}

// Inspect runs one round and returns what it found.
func (in *Inspector) Inspect(ctx context.Context) []event.FindingPayload {
	var out []event.FindingPayload
	for _, s := range in.Suspects(ctx) {
		if f, ok := in.judge(ctx, s); ok {
			out = append(out, f)
		}
	}
	in.mu.Lock()
	in.lastRun = in.opt.Now()
	in.lastFound = len(out)
	in.mu.Unlock()
	return out
}

// Suspects applies the rules to a snapshot.
func (in *Inspector) Suspects(ctx context.Context) []Suspect {
	st := in.Log.Store
	now := in.opt.Now()
	var out []Suspect
	running, err := st.Runs(ctx, store.RunFilter{Statuses: []string{event.RunStatusRunning}})
	if err != nil {
		slog.Error("inspector: list runs", "err", err)
		return nil
	}
	pending, _ := st.Approvals(ctx, "pending", "")
	waiting := map[string]bool{}
	for _, a := range pending {
		waiting[a.RunID] = true
	}
	for _, r := range running {
		if r.Paused {
			continue
		}
		evs, _ := st.RecentRunEvents(ctx, r.ID, 400)
		resumed := int64(0)
		for _, ev := range evs {
			if ev.Type == event.RunResumed {
				resumed = ev.TS
			}
		}
		flagged := map[string]bool{}
		fs, _ := st.Findings(ctx, r.ID, 50)
		for _, f := range fs {
			if f.CreatedAt > resumed {
				flagged[f.Rule] = true
			}
		}
		calls := toolCalls(evs, resumed)
		if n, last := repeats(calls); n > in.opt.Repeat && !flagged[RuleRepeat] {
			out = append(out, Suspect{Rule: RuleRepeat, Run: r, Action: event.ActionPause, Sev: event.SeverityWarn,
				Facts: fmt.Sprintf("最近连续 %d 次调用同一个工具：%s。最近的工具调用：\n%s", n, last, recent(calls, 12))})
		}
		idle := now.Sub(time.UnixMilli(r.LastEvent))
		if r.LastEvent > 0 && idle > in.opt.StalledAfter && !waiting[r.ID] && !flagged[RuleStalled] {
			out = append(out, Suspect{Rule: RuleStalled, Run: r, Action: event.ActionNotify, Sev: event.SeverityWarn,
				Facts: fmt.Sprintf("已经 %s 没有任何进展（没有在等审批）。最近的工具调用：\n%s", human(idle), recent(calls, 8))})
		}
		if age := now.Sub(time.UnixMilli(r.StartedAt)); r.StartedAt > 0 && age > in.opt.LongAfter && !flagged[RuleLong] {
			out = append(out, Suspect{Rule: RuleLong, Run: r, Action: event.ActionNotify, Sev: event.SeverityInfo,
				Facts: fmt.Sprintf("已经执行了 %s，共调用工具 %d 次。最近的工具调用：\n%s", human(age), len(calls), recent(calls, 8))})
		}
	}
	if tokens, err := st.TokensSince(ctx, now.Add(-in.opt.Every)); err == nil && tokens > in.opt.TokenAlarm {
		recentFinding := false
		fs, _ := st.Findings(ctx, "", 20)
		for _, f := range fs {
			recentFinding = recentFinding || f.Rule == RuleTokens && now.Sub(time.UnixMilli(f.CreatedAt)) < time.Hour
		}
		if !recentFinding {
			usage, _ := st.UsageSince(ctx, now.Add(-in.opt.Every))
			var b strings.Builder
			for _, u := range usage {
				fmt.Fprintf(&b, "- %s %s/%s：%d 次调用，输入 %d、输出 %d token\n", u.Role, u.Provider, u.Model, u.Calls, u.InputTokens, u.OutputTokens)
			}
			out = append(out, Suspect{Rule: RuleTokens, Action: event.ActionNotify, Sev: event.SeverityWarn,
				Facts: fmt.Sprintf("最近 %s 用掉了 %d 个 token（警戒线 %d）：\n%s", human(in.opt.Every), tokens, in.opt.TokenAlarm, b.String())})
		}
	}
	return out
}

type call struct{ title, kind string }

// toolCalls lists the run's tool calls since resumed, in order: each tool
// call id counts once, under the title it was first reported with.
func toolCalls(evs []event.Event, since int64) []call {
	seen := map[string]bool{}
	var out []call
	for _, ev := range evs {
		if ev.Type != event.RunUpdate || ev.TS < since {
			continue
		}
		var p event.RunUpdatePayload
		if ev.Decode(&p) != nil || p.Kind != "tool" || p.ToolCallID == "" || seen[p.ToolCallID] {
			continue
		}
		seen[p.ToolCallID] = true
		out = append(out, call{title: strings.TrimSpace(p.Title), kind: p.ToolKind})
	}
	return out
}

// repeats counts the identical calls at the end of the list.
func repeats(calls []call) (int, string) {
	if len(calls) == 0 {
		return 0, ""
	}
	last := calls[len(calls)-1]
	n := 0
	for i := len(calls) - 1; i >= 0 && calls[i] == last; i-- {
		n++
	}
	return n, fmt.Sprintf("%s（%s）", last.title, last.kind)
}

func recent(calls []call, n int) string {
	if len(calls) > n {
		calls = calls[len(calls)-n:]
	}
	if len(calls) == 0 {
		return "（没有）"
	}
	var b strings.Builder
	for _, c := range calls {
		fmt.Fprintf(&b, "- [%s] %s\n", c.kind, c.title)
	}
	return strings.TrimRight(b.String(), "\n")
}

func human(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1f 小时", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	}
	return fmt.Sprintf("%d 秒", int(d.Seconds()))
}

// judge asks the model about a suspect (or applies the rule's default),
// carries out the action, and records the finding.
func (in *Inspector) judge(ctx context.Context, s Suspect) (event.FindingPayload, bool) {
	st := in.Log.Store
	f := event.FindingPayload{FindingID: ids.New(), Rule: s.Rule, Severity: s.Sev, Action: s.Action}
	subject := "token 用量"
	var t store.Task
	if s.Run.ID != "" {
		subject = fmt.Sprintf("%s 的 Run", s.Run.Agent)
		if tt, err := st.Task(ctx, s.Run.TaskID); err == nil {
			t = tt
			subject = fmt.Sprintf("任务「%s」的 Run（%s）", t.Title, s.Run.Agent)
		}
	}
	reason := subject + "：" + firstLine(s.Facts)
	if in.Provider != nil {
		var out struct{ Severity, Action, Reason string }
		input := fmt.Sprintf("## 规则\n%s\n\n## 对象\n%s\n\n## 规则看到的情况\n%s\n", s.Rule, subject, s.Facts)
		if t.ID != "" {
			input += fmt.Sprintf("\n## 任务说明\n%s\n\n%s\n", t.Title, t.Detail)
		}
		if err := in.ask(ctx, t, input, &out); err != nil {
			slog.Warn("inspector: model failed; using the rule's action", "rule", s.Rule, "err", err)
		} else {
			if out.Action == "none" {
				slog.Info("inspector: the model saw no problem", "rule", s.Rule, "run", s.Run.ID, "reason", out.Reason)
				// Remember it, so the same thing is not asked about every round.
				f.Action, f.Severity, f.Reason = "none", event.SeverityInfo, strings.TrimSpace(out.Reason)
				in.record(ctx, s, f, false)
				return f, false
			}
			if valid(out.Action, event.ActionNotify, event.ActionPause, event.ActionStop) {
				f.Action = out.Action
			}
			if valid(out.Severity, event.SeverityInfo, event.SeverityWarn, event.SeverityCritical) {
				f.Severity = out.Severity
			}
			if r := strings.TrimSpace(out.Reason); r != "" {
				reason = r
			}
		}
	}
	if s.Run.ID == "" && f.Action != event.ActionNotify {
		f.Action = event.ActionNotify // nothing to pause
	}
	f.Reason = reason
	switch f.Action {
	case event.ActionPause:
		f.Done = in.Runs.Pause(ctx, s.Run.ID, "巡检："+reason, event.ActorInspector) == nil
	case event.ActionStop:
		f.Done = in.Runs.Cancel(ctx, s.Run.ID, "巡检终止："+reason) == nil
	default:
		f.Done = true
	}
	in.record(ctx, s, f, true)
	return f, true
}

func valid(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// record stores the finding and, when the user should know, a note in the
// main thread (requirements R1.5). The finding sends the push.
func (in *Inspector) record(ctx context.Context, s Suspect, f event.FindingPayload, tell bool) {
	st := in.Log.Store
	main, err := st.MainThreadID(ctx)
	if err != nil {
		slog.Error("inspector: main thread", "err", err)
		return
	}
	// Look the topic up before Write: the store has one connection, so a
	// read inside the write transaction would wait on itself forever.
	topic := st.TaskTopic(ctx, main, s.Run.TaskID)
	_, err = in.Log.Write(ctx, func(w *store.Writer) error {
		ev := event.New(event.InspectorFinding, event.ActorInspector, main, f)
		ev.RunID, ev.TaskID = s.Run.ID, s.Run.TaskID
		if _, err := w.Append(ev); err != nil {
			return err
		}
		if !tell {
			return nil
		}
		msg := event.New(event.MessageAssistant, event.ActorInspector, main,
			event.MessagePayload{MessageID: ids.New(), Content: note(f), Kind: event.KindInspector, TopicID: topic})
		msg.RunID, msg.TaskID = s.Run.ID, s.Run.TaskID
		_, err := w.Append(msg)
		return err
	})
	if err != nil {
		slog.Error("inspector: record finding", "err", err)
		return
	}
	slog.Info("inspector: finding", "rule", f.Rule, "action", f.Action, "done", f.Done, "run", s.Run.ID)
}

func note(f event.FindingPayload) string {
	text := "巡检发现：" + f.Reason
	switch {
	case f.Action == event.ActionPause && f.Done:
		text += "\n\n已经暂停了这个 Run。可以在 Run 卡片上点“继续”（或者直接告诉我怎么改），也可以取消它。"
	case f.Action == event.ActionStop && f.Done:
		text += "\n\n已经终止了这个 Run。"
	case f.Action != event.ActionNotify && !f.Done:
		text += "\n\n（本来要" + map[string]string{event.ActionPause: "暂停", event.ActionStop: "终止"}[f.Action] + "它，但没有成功，请你看一下。）"
	}
	return text
}

func (in *Inspector) ask(ctx context.Context, t store.Task, input string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := in.Provider.Stream(ctx, provider.Request{
		Model: in.Model, System: prompts.Inspector, Messages: []provider.Message{provider.Text(provider.RoleUser, input)},
	}, nil)
	if err != nil {
		return err
	}
	ev := event.New(event.UsageRecorded, event.ActorInspector, t.OriginThreadID, event.UsagePayload{
		Provider: in.Provider.Name(), Model: provider.ModelOf(resp, in.Model), Role: "inspector",
		InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens,
	})
	ev.TaskID = t.ID
	in.Log.Append(ctx, ev)
	text := resp.Message.PlainText()
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j < i {
		return fmt.Errorf("inspector 没有返回 JSON：%q", text)
	}
	return json.Unmarshal([]byte(text[i:j+1]), out)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, "。：:") + "。"
}

// Routes serves GET /api/v1/findings?run_id=&limit=.
func (in *Inspector) Routes(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/findings", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		fs, err := in.Log.Store.Findings(r.Context(), r.URL.Query().Get("run_id"), limit)
		if err != nil {
			slog.Error("inspector: list findings", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"findings": fs})
	})
}

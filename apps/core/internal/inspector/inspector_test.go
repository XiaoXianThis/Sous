package inspector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

type fakeRuns struct {
	mu      sync.Mutex
	paused  []string
	stopped []string
}

func (f *fakeRuns) Pause(_ context.Context, id, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, id)
	return nil
}

func (f *fakeRuns) Cancel(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

type answer string

func (a answer) Name() string { return "canned" }
func (a answer) Stream(context.Context, provider.Request, func(provider.Delta)) (provider.Response, error) {
	return provider.Response{Message: provider.Text(provider.RoleAssistant, string(a))}, nil
}

func setup(t *testing.T) (*eventlog.Log, string) {
	st, _ := store.Open(":memory:")
	t.Cleanup(func() { st.Close() })
	log := eventlog.New(st, event.NewBus())
	main, _ := log.EnsureMainThread(context.Background())
	return log, main
}

// running adds a running run with the given tool call titles.
func running(t *testing.T, log *eventlog.Log, titles ...string) string {
	q := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{NodeID: "n", Agent: "claude", Kind: "general", Prompt: "p"})
	q.RunID = ids.New()
	s := event.New(event.RunStarted, event.ActorSystem, "", event.RunStartedPayload{Workdir: "/w"})
	s.RunID = q.RunID
	evs := []event.Event{q, s}
	for i, title := range titles {
		u := event.New(event.RunUpdate, event.ActorSystem, "", event.RunUpdatePayload{Kind: "tool", ToolCallID: fmt.Sprint("t", i), Title: title, ToolKind: "read"})
		u.RunID = q.RunID
		evs = append(evs, u)
	}
	if _, err := log.Append(context.Background(), evs...); err != nil {
		t.Fatal(err)
	}
	return q.RunID
}

func TestRulesWithoutModel(t *testing.T) {
	log, main := setup(t)
	ctx := context.Background()
	loop := running(t, log, "Read a", "Read b", "Read c", "Read c", "Read c", "Read c", "Read c", "Read c")
	fine := running(t, log, "Read a", "Edit b", "Run tests", "Read c", "Read c")
	runs := &fakeRuns{}
	in := New(log, runs, nil, "", Options{})
	found := in.Inspect(ctx)
	if len(found) != 1 || found[0].Rule != RuleRepeat || found[0].Action != event.ActionPause || !strings.Contains(found[0].Reason, "6 次") {
		t.Fatalf("findings: %+v", found)
	}
	if len(runs.paused) != 1 || runs.paused[0] != loop {
		t.Fatalf("paused: %v (fine run %s)", runs.paused, fine)
	}
	msgs, _ := log.Store.Messages(ctx, main, 0, 10)
	if len(msgs) != 1 || msgs[0].Kind != event.KindInspector {
		t.Fatalf("main thread: %+v", msgs)
	}
	// Once found, not again until the run is resumed.
	if again := in.Inspect(ctx); len(again) != 0 {
		t.Fatalf("again: %+v", again)
	}
}

func TestStalledAndTokens(t *testing.T) {
	log, _ := setup(t)
	ctx := context.Background()
	running(t, log, "Read a")
	u := event.New(event.UsageRecorded, event.ActorSupervisor, "", event.UsagePayload{Provider: "p", Model: "m", Role: "supervisor", InputTokens: 3_000_000})
	log.Append(ctx, u)
	now := time.Now().Add(time.Hour)
	in := New(log, &fakeRuns{}, nil, "", Options{Now: func() time.Time { return now }, Every: 2 * time.Hour})
	rules := map[string]bool{}
	for _, f := range in.Inspect(ctx) {
		rules[f.Rule] = true
		if f.Action != event.ActionNotify {
			t.Fatalf("finding %+v should only notify", f)
		}
	}
	if !rules[RuleStalled] || !rules[RuleTokens] {
		t.Fatalf("rules: %v", rules)
	}
}

func TestModelSaysNone(t *testing.T) {
	log, main := setup(t)
	ctx := context.Background()
	running(t, log, "Read c", "Read c", "Read c", "Read c", "Read c", "Read c")
	runs := &fakeRuns{}
	in := New(log, runs, answer(`{"severity":"info","action":"none","reason":"在轮询构建结果，正常"}`), "m", Options{})
	if found := in.Inspect(ctx); len(found) != 0 || len(runs.paused) != 0 {
		t.Fatalf("found %+v paused %v", found, runs.paused)
	}
	if msgs, _ := log.Store.Messages(ctx, main, 0, 10); len(msgs) != 0 {
		t.Fatalf("the user was told: %+v", msgs)
	}
	// The verdict is remembered: the model is not asked again next round.
	if fs, _ := log.Store.Findings(ctx, "", 10); len(fs) != 1 || fs[0].Action != "none" {
		t.Fatalf("findings: %+v", fs)
	}
	evs, _ := log.Store.EventsSince(ctx, 0, 100)
	for _, ev := range evs {
		if event.PushKind(ev) != "" {
			t.Fatalf("pushed: %s", ev.Type)
		}
	}
}

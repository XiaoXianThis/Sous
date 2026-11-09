package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

type toolFixture struct {
	calls  int
	repeat bool
	final  bool
	onCall func()
}

func (*toolFixture) Name() string { return "tool-fixture" }
func (m *toolFixture) Stream(_ context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	m.calls++
	if m.onCall != nil {
		m.onCall()
		m.onCall = nil
	}
	if m.final || len(req.Tools) == 0 {
		return provider.Response{Message: provider.Text(provider.RoleAssistant, `{"decision":"ask_user","confidence":"high","reason":"tool result is insufficient"}`)}, nil
	}
	i := m.calls
	if m.repeat {
		i = 1
	}
	c := provider.ToolCall{ID: fmt.Sprint(i), Name: "read_file", Args: json.RawMessage(fmt.Sprintf(`{"path":"fixture-%d"}`, i))}
	return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{{ToolCall: &c}}}}, nil
}

func TestApprovalToolLoopPersistentBudgetsAndReplay(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			e := newApproveEnv(t, "")
			ctx := context.Background()
			started := event.New(event.RunStarted, event.ActorSystem, "", event.RunStartedPayload{Workdir: "fixture"})
			started.RunID = e.runID
			if _, err := e.log.Append(ctx, started); err != nil {
				t.Fatal(err)
			}
			id := e.boundRequest("", "ai", "tool fixture", "")
			m := &toolFixture{repeat: repeat}
			e.eng.Approver = task.Model{Provider: m, Name: "fixture"}
			a := e.approve(id)
			if m.calls != 6 || a.Status != "pending" || a.Review != "user" || !strings.Contains(a.Escalation, "查过：") {
				t.Fatalf("loop calls=%d approval=%+v", m.calls, a)
			}
			evs, _ := e.log.Store.EventsSince(ctx, 0, 1000)
			checks := map[string]bool{}
			for _, ev := range evs {
				if ev.Type == event.ApprovalChecked {
					var p event.ApprovalCheckPayload
					ev.Decode(&p)
					checks[p.CheckID] = true
				}
			}
			want := 4
			if repeat {
				want = 1
			}
			if len(checks) != want {
				t.Fatalf("executed reservations=%d want %d", len(checks), want)
			}
			if err := e.eng.Approve(ctx, store.Effect{Key: id, Attempts: 1}); err != nil {
				t.Fatal(err)
			}
			if m.calls != 6 {
				t.Fatal("replay reset model budget")
			}
		})
	}
}

func TestHumanDecisionDuringApprovalCallWins(t *testing.T) {
	e := newApproveEnv(t, "")
	id := e.boundRequest("", "ai", "race", "")
	m := &toolFixture{final: true, onCall: func() {
		if err := e.eng.Nodes.Decide(context.Background(), id, "no", "human answer"); err != nil {
			t.Fatal(err)
		}
	}}
	e.eng.Approver = task.Model{Provider: m, Name: "fixture"}
	a := e.approve(id)
	if a.DecidedBy != event.ActorUser || a.OptionID != "no" || a.Note != "human answer" {
		t.Fatalf("human overwritten: %+v", a)
	}
}

package task_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// canned answers the approver with the same JSON every time and keeps what
// it was asked.
type canned struct {
	mu     sync.Mutex
	answer string
	inputs []string
}

func (c *canned) Name() string { return "canned" }
func (c *canned) Stream(_ context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs = append(c.inputs, req.Messages[0].PlainText())
	return provider.Response{Message: provider.Text(provider.RoleAssistant, c.answer)}, nil
}

func (c *canned) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs)
}

type approveEnv struct {
	t      *testing.T
	log    *eventlog.Log
	eng    *task.Engine
	model  *canned
	runID  string
	taskID string
}

func newApproveEnv(t *testing.T, answer string) *approveEnv {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := eventlog.New(st, event.NewBus())
	nodes, _ := nodehub.New(log, t.TempDir())
	eng := task.New(log, nodes)
	m := &canned{answer: answer}
	eng.Approver = task.Model{Provider: m, Name: "a"}
	e := &approveEnv{t: t, log: log, eng: eng, model: m, taskID: ids.New()}
	e.runID = e.newRun()
	return e
}

func (e *approveEnv) newRun() string {
	tk := event.New(event.TaskCreated, event.ActorUser, "", event.TaskCreatedPayload{Kind: "general", Title: "改登录页", Status: "running"})
	tk.TaskID = e.taskID
	q := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{NodeID: "n", Agent: "a", Kind: "general", Prompt: "p", PermissionMode: "auto"})
	q.RunID, q.TaskID = ids.New(), e.taskID
	evs := []event.Event{q}
	if e.runID == "" {
		evs = []event.Event{tk, q}
	}
	if _, err := e.log.Append(context.Background(), evs...); err != nil {
		e.t.Fatal(err)
	}
	return q.RunID
}

// request files an approval the approver may decide.
func (e *approveEnv) request(category, title string) string {
	e.t.Helper()
	id := ids.New()
	ev := event.New(event.ApprovalRequested, event.ActorSystem, "", event.ApprovalRequestedPayload{
		ApprovalID: id, Title: title, ToolKind: "execute", Detail: "detail of " + title, Category: category, Review: "ai",
		Options: []event.ApprovalOption{{OptionID: "yes", Name: "批准", Kind: "allow_once"}, {OptionID: "no", Name: "拒绝", Kind: "reject_once"}},
	})
	ev.RunID, ev.TaskID = e.runID, e.taskID
	if _, err := e.log.Append(context.Background(), ev); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *approveEnv) approve(id string) store.Approval {
	e.t.Helper()
	if err := e.eng.Approve(context.Background(), store.Effect{Key: id}); err != nil {
		e.t.Fatal(err)
	}
	a, err := e.log.Store.Approval(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func TestApproverAllowsWithConfidence(t *testing.T) {
	e := newApproveEnv(t, `{"decision":"allow","confidence":"high","reason":"构建需要"}`)
	a := e.approve(e.request("exec-other", "npm run lint"))
	if a.Status != "decided" || a.OptionID != "yes" || a.DecidedBy != event.ActorApprover || a.Note != "构建需要" {
		t.Fatalf("approval: %+v", a)
	}
	in := e.model.inputs[0]
	if !strings.Contains(in, "改登录页") || !strings.Contains(in, "npm run lint") || !strings.Contains(in, "exec-other") {
		t.Errorf("the approver was not shown the task and the operation:\n%s", in)
	}
}

func TestApproverNotSureGoesToUser(t *testing.T) {
	for _, conf := range []string{"medium", "low"} {
		e := newApproveEnv(t, `{"decision":"allow","confidence":"`+conf+`","reason":"可能需要"}`)
		a := e.approve(e.request("install", "npm i left-pad"))
		if a.Status != "pending" || a.Review != "user" || !strings.Contains(a.Escalation, "把握不够") {
			t.Fatalf("%s: %+v", conf, a)
		}
	}
	// Anything it says that is not allow or deny is also the user's.
	for _, ans := range []string{`{"decision":"ask_user","reason":"和任务无关"}`, `{"decision":"escalate","reason":"x"}`, `{"decision":"allow please"}`} {
		e := newApproveEnv(t, ans)
		if a := e.approve(e.request("exec-other", "x")); a.Status != "pending" || a.Review != "user" {
			t.Fatalf("%s: %+v", ans, a)
		}
	}
}

func TestApproverCanRefuse(t *testing.T) {
	e := newApproveEnv(t, `{"decision":"deny","confidence":"high","reason":"和登录页无关，不要动 ~/.config"}`)
	a := e.approve(e.request("outside-worktree", "cat ~/.config/x"))
	if a.Status != "decided" || a.OptionID != "no" || a.DecidedBy != event.ActorApprover || !strings.Contains(a.Note, "无关") {
		t.Fatalf("approval: %+v", a)
	}
	// A refusal it is unsure about is the user's to make.
	e = newApproveEnv(t, `{"decision":"deny","confidence":"low","reason":"不确定"}`)
	if a := e.approve(e.request("exec-other", "x")); a.Status != "pending" || a.Review != "user" {
		t.Fatalf("unsure refusal: %+v", a)
	}
}

func TestApproverNeverDecidesIrreversible(t *testing.T) {
	for _, cat := range []string{"irreversible", "floor"} {
		e := newApproveEnv(t, `{"decision":"allow","confidence":"high","reason":"ok"}`)
		a := e.approve(e.request(cat, "git push --force"))
		if a.Status != "pending" || a.Review != "user" || e.model.calls() != 0 {
			t.Fatalf("%s: %+v, %d model calls", cat, a, e.model.calls())
		}
	}
}

func TestApproverFollowsTheUsersEarlierAnswer(t *testing.T) {
	e := newApproveEnv(t, `{"decision":"ask_user","reason":"?"}`)
	ctx := context.Background()
	first := e.request("exec-other", "./scripts/gen.sh")
	if err := e.eng.Nodes.DecideAs(ctx, first, "yes", "", event.ActorUser); err != nil {
		t.Fatal(err)
	}
	second := e.approve(e.request("exec-other", "./scripts/gen.sh"))
	if second.Status != "decided" || second.OptionID != "yes" || e.model.calls() != 0 {
		t.Fatalf("second: %+v, %d model calls", second, e.model.calls())
	}
	// The user refused another operation: the same request is refused again.
	bad := e.request("delete", "rm -rf data")
	e.eng.Nodes.DecideAs(ctx, bad, "no", "", event.ActorUser)
	again := e.approve(e.request("delete", "rm -rf data"))
	if again.OptionID != "no" || e.model.calls() != 0 {
		t.Fatalf("repeat of a refused operation: %+v", again)
	}
	// A different operation still goes to the model.
	other := e.approve(e.request("exec-other", "./scripts/other.sh"))
	if other.Status != "pending" || e.model.calls() != 1 {
		t.Fatalf("other: %+v, %d model calls", other, e.model.calls())
	}
}

func TestApproverStopsAfterRepeatedRefusals(t *testing.T) {
	e := newApproveEnv(t, `{"decision":"deny","confidence":"high","reason":"不行"}`)
	for i := 0; i < 3; i++ {
		if a := e.approve(e.request("exec-other", "curl evil")); a.OptionID != "no" {
			t.Fatalf("refusal %d: %+v", i, a)
		}
	}
	a := e.approve(e.request("exec-other", "curl evil"))
	if a.Status != "pending" || a.Review != "user" || !strings.Contains(a.Escalation, "拒绝") || e.model.calls() != 3 {
		t.Fatalf("fourth try: %+v, %d model calls", a, e.model.calls())
	}
}

func TestApproverHasABudgetPerRun(t *testing.T) {
	e := newApproveEnv(t, `{"decision":"allow","confidence":"high","reason":"ok"}`)
	ctx := context.Background()
	for i := 0; i < 150; i++ {
		id := e.request("exec-other", "step "+string(rune('a'+i%26))+string(rune('a'+i/26)))
		if err := e.eng.Nodes.DecideAs(ctx, id, "yes", "ok", event.ActorApprover); err != nil {
			t.Fatal(err)
		}
	}
	a := e.approve(e.request("exec-other", "one more"))
	if a.Status != "pending" || a.Review != "user" || !strings.Contains(a.Escalation, "150") || e.model.calls() != 0 {
		t.Fatalf("over budget: %+v", a)
	}
	// A new run has its own budget.
	e.runID = e.newRun()
	if a := e.approve(e.request("exec-other", "fresh run")); a.Status != "decided" {
		t.Fatalf("new run: %+v", a)
	}
}

func TestApproverWithoutModelEscalates(t *testing.T) {
	e := newApproveEnv(t, "")
	e.eng.Approver = task.Model{}
	if a := e.approve(e.request("exec-other", "x")); a.Review != "user" || !strings.Contains(a.Escalation, "没有配置审批模型") {
		t.Fatalf("no model: %+v", a)
	}
}

func TestApproverMediumRefusalIsFinal(t *testing.T) {
	e := newApproveEnv(t, ` {"decision":"deny","confidence":"medium","reason":"先使用项目已有脚本"}`)
	a := e.approve(e.request("exec-other", "x"))
	if a.Status != "decided" || a.OptionID != "no" || a.DecidedBy != event.ActorApprover || a.Escalation != "" {
		t.Fatalf("medium refusal: %+v", a)
	}
}

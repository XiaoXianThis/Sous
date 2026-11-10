package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

type dispatchStub struct {
	name    string
	content string
	err     error
	n       int
}

func (d *dispatchStub) Spec() provider.ToolSpec {
	return provider.ToolSpec{Name: d.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (d *dispatchStub) Call(context.Context, json.RawMessage) (string, error) {
	d.n++
	if d.content != "" {
		return d.content, d.err
	}
	return "任务 t1，排队中（等待机器上线）", d.err
}

func dispatchResponse(args string) provider.Response {
	return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{{ToolCall: &provider.ToolCall{
		ID: "create", Name: "task_create", Args: json.RawMessage(args),
	}}}}}
}

func TestDispatchClaimMustHaveReceipt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first *provider.Response
		err   error
	}{
		{name: "no-call"},
		{name: "failed-call", first: ptrResponse(dispatchResponse(`{}`)), err: errors.New("机器不存在")},
		{name: "planned-only", first: ptrResponse(dispatchResponse(`{"dispatch":false}`))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := provider.Response{Message: provider.Text(provider.RoleAssistant, "已派发给 opencode。")}
			responses := []provider.Response{claim, claim}
			if tc.first != nil {
				responses = append([]provider.Response{*tc.first}, responses...)
			}
			p := &scripted{responses: responses}
			d := &dispatchStub{name: "task_create", err: tc.err}
			log, main, _ := setup(t, p, tool.NewRegistry(d))
			ch, stop := log.Bus.Subscribe(100)
			defer stop()
			say(t, log, main, "开始执行")
			msgs := waitMessages(t, log, main, 2)
			if msgs[1].Content != dispatchUnconfirmed {
				t.Fatalf("unverified reply: %+v", msgs)
			}
			for len(ch) > 0 {
				ev := <-ch
				if ev.Type == event.MessageAssistantDelta {
					var delta event.DeltaPayload
					ev.Decode(&delta)
					if strings.Contains(delta.Delta, "已派发给") {
						t.Fatalf("unverified claim streamed: %+v", delta)
					}
				}
			}
		})
	}
}

func ptrResponse(r provider.Response) *provider.Response { return &r }

func TestDispatchClaimCanRecoverByCallingTool(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		{Message: provider.Text(provider.RoleAssistant, "已派发给 opencode。")},
		dispatchResponse(`{}`),
		{Message: provider.Text(provider.RoleAssistant, "任务 t1 正在排队，等待机器上线。")},
	}}
	d := &dispatchStub{name: "task_create"}
	log, main, _ := setup(t, p, tool.NewRegistry(d))
	say(t, log, main, "开始执行")
	msgs := waitMessages(t, log, main, 2)
	if !strings.Contains(msgs[1].Content, "任务 t1 正在排队") {
		t.Fatalf("reply: %+v", msgs)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 3 || d.n != 1 || !strings.Contains(said(p.requests[1].Messages[len(p.requests[1].Messages)-1]), "派发核验") {
		t.Fatalf("requests=%d calls=%d", len(p.requests), d.n)
	}
}

func TestDispatchClaimWithToolWaitsForResult(t *testing.T) {
	r := dispatchResponse(`{}`)
	r.Message.Parts = append([]provider.Part{{Text: "已派发给 opencode。"}}, r.Message.Parts...)
	p := &scripted{responses: []provider.Response{r, {Message: provider.Text(provider.RoleAssistant, "任务 t1 排队中。")}}}
	d := &dispatchStub{name: "task_create"}
	log, main, _ := setup(t, p, tool.NewRegistry(d))
	say(t, log, main, "开始执行")
	msgs := waitMessages(t, log, main, 2)
	if msgs[1].Content != "任务 t1 排队中。" {
		t.Fatalf("premature claim was stored: %+v", msgs)
	}
}

func TestDispatchReceiptAndPlans(t *testing.T) {
	for _, text := range []string{"准备派发任务。", "还没派发，等待你的确认。", "派发失败，机器不存在。", "任务已创建，暂未派发。", "任务已创建但未派发。", "已经准备派发。", "上次说的“已派发”没有证据。", "已交给项目顾问分析。"} {
		if claimsDispatch(text) {
			t.Errorf("plan or failure matched: %q", text)
		}
	}
	for _, text := range []string{"已派发。", "已经把两个任务派给前端。", "任务已经交给 opencode。", "任务派好了。"} {
		if !claimsDispatch(text) {
			t.Errorf("claim not matched: %q", text)
		}
	}
}

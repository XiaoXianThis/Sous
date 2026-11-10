package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

const taskOne = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
const taskTwo = "01ARZ3NDEKTSV4RRFFQ69G5FAW"

func claimEvent(t *testing.T, log *eventlog.Log, ev event.Event) {
	t.Helper()
	if _, err := log.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}
func claimTask(t *testing.T, log *eventlog.Log, id, status, reason string) {
	t.Helper()
	ev := event.New(event.TaskCreated, event.ActorSystem, "", event.TaskCreatedPayload{Title: "test", Status: status})
	ev.TaskID = id
	claimEvent(t, log, ev)
	if reason != "" {
		ev = event.New(event.TaskStatus, event.ActorSystem, "", event.TaskStatusPayload{To: status, Reason: reason})
		ev.TaskID = id
		claimEvent(t, log, ev)
	}
}
func claimRun(t *testing.T, log *eventlog.Log, id, status string, paused bool) {
	t.Helper()
	ev := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{Purpose: event.PurposeWork, NodeID: "node"})
	ev.RunID, ev.TaskID = id, taskOne
	claimEvent(t, log, ev)
	if status != event.RunStatusQueued {
		ev = event.New(event.RunStarted, event.ActorSystem, "", event.RunStartedPayload{})
		ev.RunID = id
		claimEvent(t, log, ev)
	}
	if status == event.RunStatusFailed {
		ev = event.New(event.RunEnded, event.ActorSystem, "", event.RunEndedPayload{Status: status})
		ev.RunID = id
		claimEvent(t, log, ev)
	}
	if paused {
		ev = event.New(event.RunPaused, event.ActorSystem, "", event.RunPausePayload{Reason: "paused"})
		ev.RunID = id
		claimEvent(t, log, ev)
	}
}

func TestTaskClaimsAreVerifiedPerID(t *testing.T) {
	log, _, sup := setup(t, &scripted{}, tool.NewRegistry())
	claimTask(t, log, taskOne, event.TaskQueued, "等待机器")
	v := claimVerifier{s: sup, receipts: map[string]taskReceipt{}}
	ctx := context.Background()
	v.collect(ctx, provider.ToolResult{Content: "已创建：任务 " + taskOne + "，排队中"})
	for _, text := range []string{
		"已出：" + taskTwo,
		"已派发：" + taskOne + "。已启动：" + taskTwo + "。",
		"任务 " + taskOne + " 和 " + taskTwo + " dispatched。",
		"查看 " + taskTwo + " 的状态。",
	} {
		got := v.check(ctx, text)
		if !strings.Contains(got, "未能确认（未找到该任务："+taskTwo+"）") {
			t.Fatalf("unverified ID escaped: %q", got)
		}
		if again := v.check(ctx, got); again != got {
			t.Fatalf("non-idempotent:\n%s\n%s", got, again)
		}
	}
	// A failed call echoing an invalid argument is not existence evidence.
	v.collect(ctx, provider.ToolResult{Content: "没有任务 " + taskTwo, IsError: true})
	if got := v.check(ctx, "已启动："+taskTwo); !strings.Contains(got, "未找到该任务") {
		t.Fatal(got)
	}
	// Existence from a receipt alone does not prove a running task.
	v.collect(ctx, provider.ToolResult{Content: taskTwo})
	if got := v.check(ctx, "已启动："+taskTwo); !strings.Contains(got, "状态未能确认") {
		t.Fatal(got)
	}
}

func TestTaskClaimStatesAndStartAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		status, run, reason, want string
		paused                    bool
	}{
		{status: "queued", reason: "等待机器", want: "排队中，等待机器"},
		{status: "blocked", reason: "依赖失败", want: "受阻，依赖失败"},
		{status: "planned", want: "已计划"},
		{status: "running", want: "尚未收到客户端启动确认"},
		{status: "running", run: "queued", want: "Run 排队中，尚未收到客户端启动确认"},
		{status: "running", run: "failed", want: "Run failed"},
		{status: "running", run: "running", paused: true, want: "Run 已暂停"},
		{status: "running", run: "running"},
		{status: "done", want: "已完成"},
	} {
		t.Run(tc.status+"/"+tc.run+"/"+tc.want, func(t *testing.T) {
			log, _, sup := setup(t, &scripted{}, tool.NewRegistry())
			claimTask(t, log, taskOne, tc.status, tc.reason)
			if tc.run != "" {
				claimRun(t, log, "run", tc.run, tc.paused)
			}
			v := claimVerifier{s: sup, receipts: map[string]taskReceipt{}}
			for _, verb := range []string{"已派出", "已启动", "已安排", "已开工", "正在跑", "dispatched", "started"} {
				text := "任务 " + taskOne + " " + verb + "。"
				got := v.check(context.Background(), text)
				if tc.want == "" && got != text {
					t.Fatalf("confirmed run changed: %s", got)
				}
				if tc.want != "" && !strings.Contains(got, tc.want) {
					t.Fatalf("%s: %s", verb, got)
				}
				if again := v.check(context.Background(), got); again != got {
					t.Fatalf("non-idempotent: %s => %s", got, again)
				}
			}
			completed := v.check(context.Background(), "任务 "+taskOne+" 已完成。")
			if (tc.status == "done") == strings.Contains(completed, "（实际：") {
				t.Fatal(completed)
			}
		})
	}
}

func TestTaskClaimsReadCurrentStateAndLatestRun(t *testing.T) {
	log, _, sup := setup(t, &scripted{}, tool.NewRegistry())
	claimTask(t, log, taskOne, event.TaskRunning, "")
	claimRun(t, log, "old", event.RunStatusRunning, false)
	v := claimVerifier{s: sup, receipts: map[string]taskReceipt{}}
	ctx := context.Background()
	v.collect(ctx, provider.ToolResult{Content: taskOne})
	text := "任务 " + taskOne + " 已启动。"
	if got := v.check(ctx, text); got != text {
		t.Fatal(got)
	}
	claimRun(t, log, "new", event.RunStatusQueued, false)
	if got := v.check(ctx, text); !strings.Contains(got, "Run 排队中") {
		t.Fatal(got)
	}
	ev := event.New(event.TaskStatus, event.ActorSystem, "", event.TaskStatusPayload{To: event.TaskBlocked, Reason: "依赖失败"})
	ev.TaskID = taskOne
	claimEvent(t, log, ev)
	if got := v.check(ctx, text); !strings.Contains(got, "受阻，依赖失败") {
		t.Fatal(got)
	}
}

func TestTaskClaimsAreCheckedAfterSuccessfulToolAndBeforeStreaming(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		dispatchResponse(`{}`),
		respond("已派出：" + taskOne + "。已出：" + taskTwo + "。"),
	}}
	d := &dispatchStub{name: "task_create", content: "已创建：任务 " + taskOne + "，排队中"}
	log, main, _ := setup(t, p, tool.NewRegistry(d))
	claimTask(t, log, taskOne, event.TaskQueued, "等待机器")
	ch, stop := log.Bus.Subscribe(100)
	defer stop()
	say(t, log, main, "执行")
	msgs := waitMessages(t, log, main, 2)
	got := msgs[1].Content
	if !strings.Contains(got, "排队中，等待机器") || !strings.Contains(got, "未找到该任务："+taskTwo) {
		t.Fatal(got)
	}
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == event.MessageAssistantDelta {
			var d event.DeltaPayload
			ev.Decode(&d)
			if strings.Contains(d.Delta, "已出："+taskTwo) {
				t.Fatalf("false claim streamed: %s", d.Delta)
			}
		}
	}
}

func TestReceiptHistoryAndCompactionPreserveEvidence(t *testing.T) {
	good := toolNote(provider.ToolCall{Name: "task_create"}, provider.ToolResult{Content: "任务 " + taskOne + "，排队中（等待机器）\nsecret second line"})
	bad := toolNote(provider.ToolCall{Name: "git_push"}, provider.ToolResult{Content: "rejected\ndetails", IsError: true})
	msgs := []store.Message{
		{Seq: 1, Role: "user", Content: "动手"},
		{Seq: 2, Role: "assistant", ReplyTo: 1, Content: "已派出", Tools: []event.ToolNote{good, bad}},
		{Seq: 3, Role: "assistant", Content: "处理中", Tools: []event.ToolNote{{Name: "legacy"}}},
		{Seq: 4, Role: "assistant", Kind: event.KindSilent, Content: "private reason", Tools: []event.ToolNote{bad}},
		{Seq: 5, Role: "assistant", Error: true, Content: "model error", Tools: []event.ToolNote{bad}},
	}
	history := toProvider(msgs)
	var text string
	for _, m := range history {
		text += m.PlainText()
	}
	for _, want := range []string{"【本轮工具】task_create → 任务 " + taskOne, "排队中（等待机器）", "git_push → 失败：rejected", "legacy → 未保存结果"} {
		if !strings.Contains(text, want) || !strings.Contains(transcript(msgs), want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	for _, secret := range []string{"second line", "private reason", "model error"} {
		if strings.Contains(text, secret) {
			t.Fatal(text)
		}
	}
	comp := &answering{fn: reply("## 其他背景\n- 模型漏写了回执")}
	_, main, sup := setupSup(t, Options{Provider: &scripted{}, Compactor: comp, CompactorModel: "c"})
	summary, err := sup.summarize(context.Background(), main, "", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "【本轮工具 #2.1】task_create") || !strings.Contains(summary, "失败：rejected") {
		t.Fatal(summary)
	}
	next := preserveToolReceipts(summary, "新的摘要", msgs)
	again := preserveToolReceipts(next, "新的摘要", msgs)
	if next != again || strings.Count(next, "【本轮工具 #2.1】") != 1 {
		t.Fatalf("repeated baking:\n%s\n%s", next, again)
	}
}

func TestToolReceiptResultIsMaskedAndStoredAcrossTurns(t *testing.T) {
	p := &scripted{responses: []provider.Response{dispatchResponse(`{}`), respond("任务排队中。"), respond("仍需核对状态。")}}
	d := &dispatchStub{name: "task_create", content: "任务 " + taskOne + "，排队中 GH_TOKEN=secret\nignored"}
	log, main, _ := setup(t, p, tool.NewRegistry(d))
	say(t, log, main, "执行")
	msgs := waitMessages(t, log, main, 2)
	if len(msgs[1].Tools) != 1 || !strings.Contains(msgs[1].Tools[0].Result, "GH_TOKEN=***") {
		t.Fatalf("not persisted/redacted: %+v", msgs[1].Tools)
	}
	say(t, log, main, "查状态")
	waitMessages(t, log, main, 4)
	p.mu.Lock()
	defer p.mu.Unlock()
	var text string
	for _, m := range p.requests[2].Messages {
		if m.Role == provider.RoleAssistant {
			text += m.PlainText()
		}
	}
	if !strings.Contains(text, "【本轮工具】task_create → 任务 "+taskOne) || strings.Contains(text, "secret") || strings.Contains(text, "ignored") {
		t.Fatal(text)
	}
}

func TestUnidentifiedTaskCompletionCannotBeAsserted(t *testing.T) {
	_, _, sup := setup(t, &scripted{}, tool.NewRegistry())
	v := claimVerifier{s: sup, receipts: map[string]taskReceipt{}}
	text := v.check(context.Background(), "任务已完成。")
	if !strings.Contains(text, "未能确认任务已完成") || v.check(context.Background(), text) != text {
		t.Fatal(text)
	}
}

func TestNegationOnlyQualifiesItsOwnClaim(t *testing.T) {
	for _, text := range []string{"已派发任务，但尚未完成。", "已派发任务还未完成。", "任务已启动，测试失败。", "task started but tests have not finished"} {
		if !claimsDispatch(text) {
			t.Errorf("missed affirmative claim: %s", text)
		}
	}
	for _, text := range []string{"任务尚未开工。", "task has not started", "未能确认任务已派出。", "准备开工。"} {
		if claimsDispatch(text) {
			t.Errorf("negation treated as claim: %s", text)
		}
	}
}

func TestReturnedRunIDsDoNotMasqueradeAsTaskIDs(t *testing.T) {
	log, _, sup := setup(t, &scripted{}, tool.NewRegistry())
	claimTask(t, log, taskOne, event.TaskRunning, "")
	claimRun(t, log, taskTwo, event.RunStatusQueued, false)
	v := claimVerifier{s: sup, receipts: map[string]taskReceipt{}}
	ctx := context.Background()
	v.collect(ctx, provider.ToolResult{Content: "任务 " + taskOne + "；Run " + taskTwo + " 已排队"})
	text := "任务 " + taskOne + " 已启动，Run " + taskTwo + "。"
	got := v.check(ctx, text)
	if !strings.Contains(got, "（实际：Run "+taskTwo) || strings.Contains(got, "状态未能确认") {
		t.Fatal(got)
	}
	if v.check(ctx, got) != got {
		t.Fatal("run annotation is not idempotent")
	}
	ev := event.New(event.RunStarted, event.ActorSystem, "", event.RunStartedPayload{})
	ev.RunID = taskTwo
	claimEvent(t, log, ev)
	if got = v.check(ctx, text); got != text {
		t.Fatal(got)
	}
}

func TestReceiptArchiveIsBoundedAndRepeatedFoldsAreStable(t *testing.T) {
	var msgs []store.Message
	for i := 1; i <= 100; i++ {
		msgs = append(msgs, store.Message{Seq: int64(i), Role: "assistant", Tools: []event.ToolNote{{Name: "task_result", Result: fmt.Sprint(i) + strings.Repeat("证据", 100)}}})
	}
	summary := preserveToolReceipts("", "摘要", msgs)
	_, archive, _ := strings.Cut(summary, receiptArchive)
	if len([]rune(archive)) > receiptArchiveRunes || !strings.Contains(archive, olderReceipts) || !strings.Contains(archive, "【本轮工具 #100.1】") {
		t.Fatal("receipt archive lost its bound or omission marker")
	}
	if again := preserveToolReceipts(summary, "摘要", msgs); again != summary {
		t.Fatal("repeat compaction changed the receipt archive")
	}
}

func TestCorrectedClaimsAreUsedWithinTheSameTurn(t *testing.T) {
	p := &scripted{responses: []provider.Response{
		respond("已出："+taskTwo+"。", provider.ToolCall{ID: "read", Name: "count", Args: []byte(`{}`)}),
		respond("需要核对任务。"),
	}}
	log, main, _ := setup(t, p, tool.NewRegistry(&counter{}))
	say(t, log, main, "查看任务")
	waitMessages(t, log, main, 3)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.requests[1].Messages {
		if m.Role == provider.RoleAssistant && strings.Contains(m.PlainText(), "已出："+taskTwo) {
			t.Fatal("uncorrected claim replayed within the turn")
		}
	}
}

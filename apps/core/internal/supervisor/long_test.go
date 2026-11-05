package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// carryOver is a compactor that keeps every line of the old summary, notes a
// commitment it finds in the folded messages, and adds one decision.
func carryOver(req provider.Request, n int) (provider.Response, error) {
	in := req.Messages[0].PlainText()
	old := strings.TrimPrefix(in[:strings.Index(in, "\n\n要折叠的消息：")], "旧摘要：\n")
	if old == "（空）" {
		old = "## 已做的决定\n## 待办和承诺"
	}
	lines := strings.Split(old, "\n")
	add := func(section, line string) {
		for i, l := range lines {
			if l == "## "+section {
				lines = append(lines[:i+1], append([]string{line}, lines[i+1:]...)...)
				return
			}
		}
	}
	if m := regexp.MustCompile(`\[#(\d+)\] [^\n]*续费域名`).FindStringSubmatch(in); m != nil {
		add("待办和承诺", "- 周五前续费 agent.wxian.cc 域名 [#"+m[1]+"]")
	}
	add("已做的决定", fmt.Sprintf("- 第 %d 次整理 [#%d]", n+1, n+1000000))
	return provider.Response{Message: provider.Text(provider.RoleAssistant, strings.Join(lines, "\n"))}, nil
}

// A thread of hundreds of turns: no request ever passes the hard
// threshold, the thread is compacted many times, the commitment made at the
// start survives every summary, and its original can be read back.
func TestLongThread(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	const window, turns = 9000, 300
	sup := &answering{fn: reply("好")}
	comp := &answering{fn: carryOver}
	log, main := setupWith(t, Options{Provider: sup, Compactor: comp, CompactorModel: "c",
		ContextWindow: func(context.Context) int { return window }})
	converse(t, log, main, "记一下：周五前我要续费域名 agent.wxian.cc，到时提醒我")
	for i := 0; i < turns; i++ {
		converse(t, log, main, fmt.Sprintf("第 %d 轮：%s", i, strings.Repeat("闲聊", 60)))
	}
	idle(t, log, main)

	for i, r := range sup.all() {
		if n := estimateRequest(r); n > int(HardRatio*window) {
			t.Fatalf("request %d has %d tokens", i, n)
		}
	}
	ctx := context.Background()
	seg, err := log.Store.LatestSegment(ctx, main)
	if err != nil || seg.Segment < 10 {
		t.Fatalf("expected many compactions, got %+v %v", seg, err)
	}
	if !strings.Contains(seg.Summary, "续费 agent.wxian.cc") {
		t.Fatalf("the commitment was lost:\n%s", seg.Summary)
	}
	last := sup.all()[len(sup.all())-1]
	if !strings.Contains(last.Messages[0].PlainText(), "续费 agent.wxian.cc") {
		t.Fatal("the commitment is not in the head")
	}

	ref := regexp.MustCompile(`续费 agent.wxian.cc 域名 \[#(\d+)\]`).FindStringSubmatch(seg.Summary)
	read := tool.ThreadRead{Log: log}
	args, _ := json.Marshal(map[string]any{"seq": json.Number(ref[1]), "before": 0, "after": 0})
	main2, _ := log.Store.MainThreadID(ctx)
	out, err := read.Call(tool.WithTurn(ctx, tool.Turn{ThreadID: main, MainThreadID: main2}), args)
	if err != nil || !strings.Contains(out, "周五前我要续费域名") {
		t.Fatalf("read back: %q %v", out, err)
	}
	var reasons = map[string]int{}
	evs, _ := log.Store.EventsSince(ctx, 0, 100000)
	for _, ev := range evs {
		if ev.Type == event.ThreadCompacted {
			var p event.CompactedPayload
			ev.Decode(&p)
			reasons[p.Reason]++
		}
	}
	t.Logf("%d turns, %d segments, reasons %v, compactor calls %d", turns, seg.Segment, reasons, len(comp.all()))
}

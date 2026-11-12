package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
)

// note appends an assistant notice written at ts and returns its seq.
func note(t *testing.T, log *eventlog.Log, thread, text string, ts time.Time) int64 {
	t.Helper()
	ev := event.New(event.MessageAssistant, event.ActorSupervisor, thread,
		event.MessagePayload{MessageID: text, Content: text, Kind: event.KindNotice})
	ev.TS = ts.UnixMilli()
	out, err := log.Append(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	return out[0].Seq
}

func TestTidyDropsStaleCommitments(t *testing.T) {
	log, main, s := setupSup(t, Options{Provider: &answering{fn: reply("嗯")}})
	now := time.Now()
	old := note(t, log, main, "旧", now.Add(-20*24*time.Hour))
	recent := note(t, log, main, "新", now.Add(-2*24*time.Hour))
	summary := fmt.Sprintf("## 已做的决定\n- 用 Caddy 反代 [#%d]\n## 待办和承诺\n- 周五提醒用户续费域名 [#%d]\n- 整理会议纪要 [#%d]\n- 没有来源的承诺\n## 已完成或作废\n- 无", old, old, recent)

	got := s.tidy(context.Background(), main, "", summary, false)
	items := parseSummary(got)
	if sectionOf(items, "续费") != "已完成或作废" {
		t.Fatalf("stale commitment in %q:\n%s", sectionOf(items, "续费"), got)
	}
	if sectionOf(items, "会议纪要") != "待办和承诺" || sectionOf(items, "没有来源") != "待办和承诺" {
		t.Fatalf("recent or undated commitment moved:\n%s", got)
	}
	if sectionOf(items, "Caddy") != "已做的决定" {
		t.Fatalf("old decision moved without budget pressure:\n%s", got)
	}
	// The done section is discarded by the next compaction, so a guarded
	// item that moved there does not count as missing.
	if m := missingItems(summary, got); len(m) != 0 {
		t.Fatalf("missing: %+v", m)
	}
}

func TestTidyArchivesOldDecisionsOverBudget(t *testing.T) {
	log, main, s := setupSup(t, Options{Provider: &answering{fn: reply("嗯")},
		ContextWindow: func(context.Context) int { return 8000 }})
	ctx := context.Background()
	now := time.Now()
	var b strings.Builder
	b.WriteString("## 已做的决定\n")
	for i := 0; i < 40; i++ {
		seq := note(t, log, main, fmt.Sprintf("m%d", i), now.Add(-time.Duration(40-i)*time.Hour))
		fmt.Fprintf(&b, "- 第%d号决定：部署脚本放在工作目录下面并且每天凌晨备份一次 [#%d]\n", i, seq)
	}
	b.WriteString("## 用户的偏好和纠正\n- 用户喜欢简短的回答\n")
	b.WriteString("## 其他背景\n- 无")
	// One decision is already a memory: it leaves the summary, not twice.
	if _, err := log.Append(ctx, event.New(event.MemoryPut, event.ActorSupervisor, "",
		event.MemoryPayload{MemoryID: "pre", Kind: "note", Content: "对话里做过的决定：第0号决定：部署脚本放在工作目录下面并且每天凌晨备份一次"})); err != nil {
		t.Fatal(err)
	}
	summary := b.String() + receiptArchive + "【本轮工具 #1.1】ok"

	got := s.tidy(ctx, main, "", summary, true)
	if est := estimateText(prose(got)); float64(est) > SummaryTargetRatio*8000+60 {
		t.Fatalf("still %d tokens:\n%s", est, got)
	}
	if !strings.HasSuffix(got, receiptArchive+"【本轮工具 #1.1】ok") {
		t.Fatalf("receipts lost:\n%s", got)
	}
	items := parseSummary(got)
	if sectionOf(items, "第0号") != "" || sectionOf(items, "第39号") != "已做的决定" {
		t.Fatalf("want the oldest archived and the newest kept:\n%s", got)
	}
	n := archivedCount(got)
	if n == 0 {
		t.Fatalf("no archive note:\n%s", got)
	}
	notes, _ := log.Store.Memories(ctx, "note", 100)
	if len(notes) != n {
		t.Fatalf("want %d memories (one already there, %d new), got %d", n, n-1, len(notes))
	}
	if mems, _ := log.Store.SearchMemories(ctx, "第1号决定", 5); len(mems) == 0 {
		t.Fatal("archived decision not searchable")
	}

	// The next summary lost the note line: the count carries over.
	next := strings.Replace(got, archivedNote, "别的背景", 1)
	again := s.tidy(ctx, main, got, next, false)
	if archivedCount(again) != n {
		t.Fatalf("count %d, want %d:\n%s", archivedCount(again), n, again)
	}
	if strings.Count(again, archivedNote) != 1 {
		t.Fatalf("want one note line:\n%s", again)
	}
}

func sectionOf(items []item, word string) string {
	for _, it := range items {
		if strings.Contains(it.text, word) {
			return it.section
		}
	}
	return ""
}

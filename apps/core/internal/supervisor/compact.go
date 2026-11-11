package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// RoleCompactor is the usage role of compaction calls.
const RoleCompactor = "compactor"

// flushSteps bounds the model calls of the silent memory turn.
const flushSteps = 6

// foldRange picks the messages of a segment to fold into the summary: all
// but the recent ones (see KeepUsers, KeepRatio, KeepMaxRatio). msgs are the
// messages after prevCut. A reply to a folded user message is folded with
// it even when it came later, and the unanswered messages are never folded.
// cut is the new segment's CoversUpTo. The reason changes what is kept: a
// manual compaction keeps KeepUsers user messages however short they are,
// and after a context overflow, when the window is evidently smaller than
// configured, only OverflowKeepUsers are kept.
func (s *Supervisor) foldRange(ctx context.Context, msgs []store.Message, prevCut int64, reason string) (fold []store.Message, cut int64) {
	w := float64(s.window(ctx))
	keepUsers, keepRatio := KeepUsers, KeepRatio
	switch reason {
	case event.CompactManual:
		keepRatio = 0
	case event.CompactOverflow:
		keepUsers, keepRatio = OverflowKeepUsers, 0
	}
	upTo := oldestPending(msgs)
	keepFrom, users, tokens := len(msgs), 0, 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if users >= keepUsers && float64(tokens) >= keepRatio*w && (upTo == 0 || msgs[i].Seq < upTo) {
			break
		}
		if float64(tokens) >= KeepMaxRatio*w && (upTo == 0 || msgs[i].Seq < upTo) {
			break
		}
		m := msgs[i]
		if m.Role == "user" {
			users++
		}
		tokens += estimateText(plain(clip(m)).Content) + 4
		keepFrom = i
	}
	// Cut right before a user message, so a turn is not split.
	for keepFrom > 0 && keepFrom < len(msgs) && msgs[keepFrom].Role != "user" {
		keepFrom++
	}
	if keepFrom >= len(msgs) || keepFrom == 0 {
		return nil, prevCut
	}
	if upTo > 0 && msgs[keepFrom].Seq > upTo {
		return nil, prevCut
	}
	cut = msgs[keepFrom].Seq - 1
	for _, m := range msgs {
		if m.Role == "assistant" && m.ReplyTo > 0 && m.ReplyTo <= prevCut {
			continue // answers a message folded earlier
		}
		if m.Seq <= cut || (m.Role == "assistant" && m.ReplyTo > 0 && m.ReplyTo <= cut) {
			fold = append(fold, m)
		}
	}
	return fold, cut
}

// Compact folds the older messages of a thread into its summary now. It
// runs in the thread's worker, so it never overlaps a turn.
func (s *Supervisor) Compact(threadID string) {
	s.enqueue(threadID, func() {
		if err := s.compact(s.ctx, threadID, event.CompactManual); err != nil {
			slog.Warn("supervisor: manual compaction", "thread", threadID, "err", err)
		}
	})
}

// EditSummary replaces the thread's summary with the user's text, starting
// a new segment that covers the same messages.
func (s *Supervisor) EditSummary(threadID, summary string) {
	s.enqueue(threadID, func() {
		seg, err := s.segment(s.ctx, threadID)
		if err != nil {
			slog.Warn("supervisor: edit summary", "thread", threadID, "err", err)
			return
		}
		p := event.CompactedPayload{Segment: seg.Segment + 1, CoversUpTo: seg.CoversUpTo, Summary: strings.TrimSpace(summary),
			Profile: s.profile(s.ctx), Reason: event.CompactEdit}
		if _, err := s.opt.Log.Append(s.ctx, event.New(event.ThreadCompacted, event.ActorUser, threadID, p)); err != nil {
			slog.Warn("supervisor: edit summary", "thread", threadID, "err", err)
		}
	})
}

// maybeCompact compacts a thread in the background once its context has
// passed SoftRatio of the window, after a turn.
func (s *Supervisor) maybeCompact(threadID string) {
	ctx := s.ctx
	st := s.opt.Log.Store
	if s.opt.Compactor == nil {
		return // turns trim instead
	}
	thread, err := st.Thread(ctx, threadID)
	if err != nil {
		return
	}
	seg, err := st.LatestSegment(ctx, threadID)
	if err != nil {
		return
	}
	history, err := st.MessagesAfter(ctx, threadID, seg.CoversUpTo)
	if err != nil {
		return
	}
	used := estimateRequest(s.build(ctx, thread, seg, history).req)
	if u, err := st.RecentThreadUsage(ctx, threadID, "supervisor", 1); err == nil && u.LastInput > used {
		used = u.LastInput
	}
	if float64(used) < SoftRatio*float64(s.window(ctx)) {
		return
	}
	if err := s.compact(ctx, threadID, event.CompactSoft); err != nil {
		slog.Warn("supervisor: compaction", "thread", threadID, "err", err)
	}
}

// compact folds the older messages of the current segment into the summary
// and starts a new segment. Before summarizing, a silent turn saves what is
// worth keeping as memories.
func (s *Supervisor) compact(ctx context.Context, threadID, reason string) (err error) {
	s.mu.Lock()
	a := s.active[threadID]
	created := a == nil
	if created {
		a = &activity{Since: time.Now()}
		s.active[threadID] = a
	}
	a.Compacting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if created {
			delete(s.active, threadID)
		} else if current := s.active[threadID]; current == a {
			current.Compacting = false
		}
		s.mu.Unlock()
	}()
	defer func() { s.noteCompaction(threadID, err) }()
	if s.opt.Compactor == nil {
		return errors.New("no model for compaction")
	}
	st := s.opt.Log.Store
	seg, err := s.segment(ctx, threadID)
	if err != nil {
		return err
	}
	msgs, err := st.MessagesAfter(ctx, threadID, seg.CoversUpTo)
	if err != nil {
		return err
	}
	fold, cut := s.foldRange(ctx, msgs, seg.CoversUpTo, reason)
	var shown []store.Message
	for _, m := range fold {
		if (!m.Error && m.Kind != event.KindSilent && strings.TrimSpace(m.Content) != "") || len(m.Tools) > 0 {
			shown = append(shown, m)
		}
	}
	if cut <= seg.CoversUpTo {
		return nil // nothing old enough to fold
	}
	slog.Info("supervisor: compacting", "thread", threadID, "reason", reason, "messages", len(fold), "up_to", cut)
	summary := seg.Summary
	thread, err := st.Thread(ctx, threadID)
	if err != nil {
		return err
	}
	for _, chunk := range s.chunks(ctx, shown) {
		if err := func() error {
			if isMeeting(thread) {
				return nil
			}
			return s.flush(ctx, threadID, chunk)
		}(); err != nil {
			slog.Warn("supervisor: memory flush before compaction", "thread", threadID, "err", err)
		}
		next, err := s.summarize(ctx, threadID, summary, chunk)
		if err != nil {
			return err
		}
		summary = next
	}
	p := event.CompactedPayload{Segment: seg.Segment + 1, CoversUpTo: cut, Summary: summary,
		Profile: s.profile(ctx), Reason: reason, Model: s.opt.CompactorModel}
	if u, err := st.RecentThreadUsage(ctx, threadID, "supervisor", 1); err == nil {
		p.InputTokens = u.LastInput
	}
	_, err = s.opt.Log.Append(ctx, event.New(event.ThreadCompacted, event.ActorSupervisor, threadID, p))
	return err
}

// noteCompaction counts failed compactions of a thread and tells the user
// once when they keep failing.
func (s *Supervisor) noteCompaction(threadID string, err error) {
	s.mu.Lock()
	if err == nil {
		delete(s.failures, threadID)
		s.mu.Unlock()
		return
	}
	s.failures[threadID]++
	n := s.failures[threadID]
	s.mu.Unlock()
	if n != 3 {
		return
	}
	text := "（整理对话上下文连续失败了 3 次：" + err.Error() + "。在修好之前，最早的消息会按长度略去；原文都还在，可以搜索。）"
	s.opt.Log.Append(s.ctx, event.New(event.MessageAssistant, event.ActorSupervisor, threadID,
		event.MessagePayload{MessageID: newID(), Content: text, Kind: event.KindNotice}))
}

// chunks splits messages into pieces that fit a compaction call.
func (s *Supervisor) chunks(ctx context.Context, msgs []store.Message) [][]store.Message {
	limit := int(0.35 * float64(s.window(ctx)))
	var out [][]store.Message
	var cur []store.Message
	n := 0
	for _, m := range msgs {
		t := estimateText(plain(clip(m)).Content) + 8
		if len(cur) > 0 && n+t > limit {
			out = append(out, cur)
			cur, n = nil, 0
		}
		cur = append(cur, m)
		n += t
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// transcript renders messages for the compactor, each with its seq.
func transcript(msgs []store.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		who := "用户"
		switch {
		case m.Role == "assistant":
			who = "主管"
		case m.Kind == event.KindTrigger:
			who = "系统"
		}
		t := time.UnixMilli(m.CreatedAt).Format("2006-01-02 15:04")
		fmt.Fprintf(&b, "[#%d] %s %s：%s\n\n", m.Seq, t, who, plain(clip(m)).Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// flush runs the silent turn that saves memories from messages about to be
// folded. It uses the supervisor's model and only the memory tools.
func (s *Supervisor) flush(ctx context.Context, threadID string, msgs []store.Message) error {
	tools := s.opt.Tools.Subset("memory_save", "memory_search", "memory_forget")
	if len(tools.Specs()) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("即将折叠的对话：\n\n")
	b.WriteString(transcript(msgs))
	if prof := s.profile(ctx); len(prof) > 0 {
		b.WriteString("\n\n现有的关于用户的长期记忆：\n")
		for _, m := range prof {
			b.WriteString("- " + m + "\n")
		}
	}
	mainID, _ := s.opt.Log.Store.MainThreadID(ctx)
	ctx = tool.WithTurn(ctx, tool.Turn{ThreadID: threadID, MainThreadID: mainID})
	req := provider.Request{Model: s.opt.Model, SessionID: threadID + ":flush", System: prompts.MemoryFlush,
		Messages: []provider.Message{provider.Text(provider.RoleUser, b.String())}, Tools: tools.Specs()}
	for step := 0; step < flushSteps; step++ {
		resp, err := s.opt.Provider.Stream(ctx, req, nil)
		if err != nil {
			return err
		}
		s.record(threadID, RoleCompactor, provider.ModelOf(resp, s.opt.Model), s.opt.Provider, resp)
		calls := resp.Message.ToolCalls()
		if len(calls) == 0 {
			return nil
		}
		req.Messages = append(req.Messages, resp.Message)
		results := provider.Message{Role: provider.RoleTool}
		for _, c := range calls {
			r := tools.Call(ctx, c)
			results.Parts = append(results.Parts, provider.Part{ToolResult: &r})
		}
		req.Messages = append(req.Messages, results)
	}
	return nil
}

// summarize folds msgs into the summary old with the compactor's model,
// checking that no decision or commitment silently disappears.
func (s *Supervisor) summarize(ctx context.Context, threadID, old string, msgs []store.Message) (string, error) {
	var b strings.Builder
	b.WriteString("旧摘要：\n")
	if strings.TrimSpace(old) == "" {
		b.WriteString("（空）")
	} else {
		b.WriteString(old)
	}
	b.WriteString("\n\n要折叠的消息：\n\n")
	b.WriteString(transcript(msgs))
	req := provider.Request{Model: s.opt.CompactorModel, SessionID: threadID + ":compact", System: prompts.Summarizer,
		Messages: []provider.Message{provider.Text(provider.RoleUser, b.String())}}
	var summary string
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := s.opt.Compactor.Stream(ctx, req, nil)
		if err != nil {
			return "", err
		}
		s.record(threadID, RoleCompactor, provider.ModelOf(resp, s.opt.CompactorModel), s.opt.Compactor, resp)
		summary = strings.TrimSpace(resp.Message.PlainText())
		if summary == "" {
			return "", errors.New("the compactor returned an empty summary")
		}
		missing := missingItems(old, summary)
		if len(missing) == 0 {
			return preserveToolReceipts(old, summary, msgs), nil
		}
		if attempt == 0 {
			req.Messages = append(req.Messages, resp.Message, provider.Text(provider.RoleUser,
				"新摘要漏掉了旧摘要里的这些条目。它们要么原样保留在原来的栏目，要么移到“"+prompts.SummaryDone+"”并写明依据。"+
					"请重新输出完整的摘要：\n"+strings.Join(lines(missing), "\n")))
			continue
		}
		slog.Warn("supervisor: summary dropped items, putting them back", "thread", threadID, "items", len(missing))
		summary = restore(summary, missing)
	}
	return preserveToolReceipts(old, summary, msgs), nil
}

// record stores the usage of a call made for a thread.
func (s *Supervisor) record(threadID, role, model string, p provider.Provider, resp provider.Response) {
	u := resp.Usage
	ev := event.New(event.UsageRecorded, event.ActorSupervisor, threadID, event.UsagePayload{
		Provider: p.Name(), Model: model, Role: role,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens,
	})
	if _, err := s.opt.Log.Append(s.ctx, ev); err != nil {
		slog.Error("supervisor: store usage", "err", err)
	}
}

const (
	receiptArchive      = "\n\n【工具回执存档：历史结果，不代表当前状态】\n"
	receiptArchiveRunes = 6000
	olderReceipts       = "【更早工具回执已折叠：执行结果需用 thread_read 核对原消息，不能凭主管文字确认成功】"
)

// Keep exact evidence independently of the summarizer. Subsequent folds
// carry the archive once, with stable message/call references, even if the
// model omits it or repeats it. Never turn a historical receipt into state.
func preserveToolReceipts(old, summary string, msgs []store.Message) string {
	_, archive, _ := strings.Cut(old, receiptArchive)
	summary, _, _ = strings.Cut(summary, receiptArchive)
	var entries []string
	seen := map[string]bool{}
	add := func(line string) {
		if line != "" && !seen[line] {
			entries = append(entries, line)
			seen[line] = true
		}
	}
	for _, line := range strings.Split(archive, "\n") {
		if strings.HasPrefix(line, "【本轮工具 #") {
			add(line)
		}
	}
	for _, m := range msgs {
		for i, n := range m.Tools {
			text := historyText(store.Message{Role: "assistant", Tools: []event.ToolNote{n}})
			text = strings.TrimPrefix(text, "\n【本轮工具】")
			add(fmt.Sprintf("【本轮工具 #%d.%d】%s", m.Seq, i+1, text))
		}
	}
	// Keep the most recent receipts within a fixed budget. Older evidence
	// is explicitly marked unavailable, never silently replaced by claims.
	sort.SliceStable(entries, func(i, j int) bool {
		var si, sj, ci, cj int64
		fmt.Sscanf(entries[i], "【本轮工具 #%d.%d】", &si, &ci)
		fmt.Sscanf(entries[j], "【本轮工具 #%d.%d】", &sj, &cj)
		if si == sj {
			return ci < cj
		}
		return si < sj
	})
	omitted := strings.Contains(archive, olderReceipts)
	used, from := len([]rune(olderReceipts))+1, len(entries)
	for from > 0 {
		size := len([]rune(entries[from-1])) + 1
		if used+size > receiptArchiveRunes {
			break
		}
		used += size
		from--
	}
	if from > 0 {
		omitted = true
		entries = entries[from:]
	}
	if omitted {
		entries = append([]string{olderReceipts}, entries...)
	}
	// The model may have copied an archive entry into prose. Retain only
	// the code-owned copy, so repeated compactions are idempotent.
	var prose []string
	for _, line := range strings.Split(summary, "\n") {
		if !strings.Contains(line, "【本轮工具 #") {
			prose = append(prose, line)
		}
	}
	summary = strings.TrimSpace(strings.Join(prose, "\n"))
	if len(entries) == 0 {
		return summary
	}
	return summary + receiptArchive + strings.Join(entries, "\n")
}

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// The context of a thread (work/specs/long-main-thread): the model sees a
// fixed system prompt, the segment's head (profile snapshot and summary), the
// messages after the segment started, and a tail with what changes each
// turn. Within a segment requests only grow at the end, so they hit the
// provider's prompt cache; folding old messages into the summary starts a
// new segment.
const (
	// DefaultContextWindow is used when the role's model has no window set.
	DefaultContextWindow = 128_000
	// SoftRatio: after a turn whose input passed this share of the window,
	// the thread is compacted in the background.
	SoftRatio = 0.60
	// HardRatio: a turn whose input would pass this share is compacted
	// first; if that fails, the oldest messages are left out.
	HardRatio = 0.85
	// KeepUsers and KeepRatio: compaction keeps at least this many recent
	// user messages and this share of the window as raw messages...
	KeepUsers = 10
	KeepRatio = 0.25
	// ...but never more than KeepMaxRatio of the window.
	KeepMaxRatio = 0.40
	// OverflowKeepUsers is how many user messages stay after the model
	// said the context was too long.
	OverflowKeepUsers = 4
	// BigMessageRunes: older messages longer than this are shown by their
	// head and tail only.
	BigMessageRunes = 4000
	bigMessageKeep  = 1500
	// ProfileLimit is how many profile memories a segment's snapshot holds.
	ProfileLimit = 30
)

// estimateText guesses the tokens of s, erring high: a CJK character is
// about one token, other text about four characters per token.
func estimateText(s string) int {
	n := 0.0
	for _, r := range s {
		if r >= 0x2E80 {
			n += 1
		} else {
			n += 0.3
		}
	}
	return int(n) + 1
}

func estimateMessage(m provider.Message) int {
	n := 4
	for _, p := range m.Parts {
		n += estimateText(p.Text)
		if p.Image != nil {
			n += 1600 // a picture costs about this much whatever its size
		}
		if p.ToolCall != nil {
			n += estimateText(p.ToolCall.Name) + estimateText(string(p.ToolCall.Args))
		}
		if p.ToolResult != nil {
			n += estimateText(p.ToolResult.Content)
		}
	}
	return n
}

// estimateRequest guesses the input tokens of req.
func estimateRequest(req provider.Request) int {
	n := estimateText(req.System)
	for _, t := range req.Tools {
		n += estimateText(t.Name) + estimateText(t.Description) + estimateText(string(t.Schema))
	}
	for _, m := range req.Messages {
		n += estimateMessage(m)
	}
	return n
}

// clip shortens a long message to its head and tail.
func clip(m store.Message) store.Message {
	r := []rune(m.Content)
	if len(r) <= BigMessageRunes {
		return m
	}
	m.Content = string(r[:bigMessageKeep]) +
		fmt.Sprintf("\n\n【中间省略 %d 字，用 thread_read #%d 看全文】\n\n", len(r)-2*bigMessageKeep, m.Seq) +
		string(r[len(r)-bigMessageKeep:])
	return m
}

// plain is m as text: its files by name. Summaries and token counts of the
// history use it; the model itself sees the files (toProviderWith).
func plain(m store.Message) store.Message {
	m.Content = historyText(m)
	m.Tools = nil
	if len(m.Attachments) == 0 {
		return m
	}
	names := make([]string, len(m.Attachments))
	for i, a := range m.Attachments {
		names[i] = a.Name
	}
	m.Content += "\n【附件：" + strings.Join(names, "、") + "】"
	m.Attachments = nil
	return m
}

// clipAnswered clips every message the thread has already answered; the
// messages of the coming turn are shown in full.
func clipAnswered(msgs []store.Message) []store.Message {
	answered := answers(msgs)
	out := make([]store.Message, len(msgs))
	for i, m := range msgs {
		if m.Seq <= answered[m.TopicID] || m.Role == "assistant" {
			m = clip(m)
		}
		out[i] = m
	}
	return out
}

// window is the context window of the supervisor's model.
func (s *Supervisor) window(ctx context.Context) int {
	if s.opt.ContextWindow != nil {
		if w := s.opt.ContextWindow(ctx); w > 0 {
			return w
		}
	}
	return DefaultContextWindow
}

// segment returns the thread's current segment, starting the first one
// when the thread has none.
func (s *Supervisor) segment(ctx context.Context, threadID string) (store.Segment, error) {
	st := s.opt.Log.Store
	seg, err := st.LatestSegment(ctx, threadID)
	if !errors.Is(err, store.ErrNotFound) {
		return seg, err
	}
	// A thread from before segments existed may be long: the first segment
	// starts where the recent messages fit, like the fixed window did
	// before. The older ones have no summary but can still be searched.
	msgs, err := st.MessagesAfter(ctx, threadID, 0)
	if err != nil {
		return store.Segment{}, err
	}
	p := event.CompactedPayload{Segment: 1, Reason: event.CompactStart, Profile: s.profile(ctx)}
	total := 0
	for _, m := range msgs {
		total += estimateText(plain(clip(m)).Content) + 4
	}
	if fold, cut := s.foldRange(ctx, msgs, 0, event.CompactStart); total > int(SoftRatio*float64(s.window(ctx))) && len(fold) > 0 {
		p.CoversUpTo = cut
		p.Summary = fmt.Sprintf("## 其他背景\n- 更早的对话（#%d 到 #%d）在启用摘要之前，没有整理进摘要；需要时用 thread_search 查原文。",
			fold[0].Seq, cut)
	}
	if _, err := s.opt.Log.Append(ctx, event.New(event.ThreadCompacted, event.ActorSupervisor, threadID, p)); err != nil {
		return store.Segment{}, err
	}
	return st.LatestSegment(ctx, threadID)
}

// profile is the snapshot of the user's profile memories.
func (s *Supervisor) profile(ctx context.Context) []string {
	mems, err := s.opt.Log.Store.Memories(ctx, "profile", ProfileLimit)
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(mems))
	for _, m := range mems {
		out = append(out, tool.FormatMemory(m))
	}
	return out
}

// memoryID is the id in a formatted memory "[id] (kind) content".
func memoryID(formatted string) string {
	if !strings.HasPrefix(formatted, "[") {
		return ""
	}
	if i := strings.Index(formatted, "]"); i > 0 {
		return formatted[1:i]
	}
	return ""
}

// profileDiff compares the current profile with a segment's snapshot.
func profileDiff(snapshot, current []string) (changed, gone []string) {
	had := map[string]bool{}
	for _, m := range snapshot {
		had[m] = true
	}
	now := map[string]bool{}
	for _, m := range current {
		now[memoryID(m)] = true
		if !had[m] {
			changed = append(changed, m)
		}
	}
	for _, m := range snapshot {
		if id := memoryID(m); id != "" && !now[id] {
			gone = append(gone, id)
		}
	}
	return changed, gone
}

// withoutDMs drops the user's direct messages to agents and their answers:
// they are not addressed to the supervisor, which only gets a summary of
// them in the tail (decision 0028).
func withoutDMs(msgs []store.Message) []store.Message {
	out := make([]store.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Kind == event.KindDM {
			continue
		}
		out = append(out, m)
	}
	return out
}

// dmLines summarizes the direct messages of the current segment by agent
// and task, newest first, at most eight lines.
func (s *Supervisor) dmLines(ctx context.Context, after int64, msgs []store.Message) []string {
	type group struct {
		task, agent string
		count       int
		last        store.Message
	}
	idx := map[string]int{}
	var groups []group
	for _, m := range msgs {
		if m.Seq <= after || m.Role != "user" || m.Kind != event.KindDM {
			continue
		}
		agent := ""
		if m.Source != nil {
			agent = m.Source.Agent
		}
		key := m.TaskID + "\x00" + agent
		i, ok := idx[key]
		if !ok {
			i = len(groups)
			idx[key] = i
			groups = append(groups, group{task: m.TaskID, agent: agent})
		}
		groups[i].count++
		if m.Seq >= groups[i].last.Seq {
			groups[i].last = m
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].last.Seq > groups[j].last.Seq })
	if len(groups) > 8 {
		groups = groups[:8]
	}
	titles := map[string]string{}
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		when := time.Unix(g.last.CreatedAt, 0).Format("15:04")
		if g.task == "" {
			out = append(out, fmt.Sprintf("项目顾问：%d 条，最近一次 %s（#%d）", g.count, when, g.last.Seq))
			continue
		}
		title := titles[g.task]
		if title == "" {
			if t, err := s.opt.Log.Store.Task(ctx, g.task); err == nil {
				title = t.Title
			}
			titles[g.task] = title
		}
		if title == "" {
			title = "任务"
		}
		out = append(out, fmt.Sprintf("%s · 任务 %s「%s」：%d 条，最近一次 %s（#%d）", g.agent, g.task, title, g.count, when, g.last.Seq))
	}
	return out
}

// built is a turn's request and where its parts are.
type built struct {
	req      provider.Request
	head     int // index of the segment head message, -1 without one
	lastUser int // index of the message carrying the tail, -1 without one
}

// build assembles the request of a turn from the segment and its messages.
func (s *Supervisor) build(ctx context.Context, thread store.Thread, seg store.Segment, history []store.Message) built {
	project := ""
	if thread.ProjectID != "" {
		project = thread.ProjectID
		if s.opt.DescribeContext != nil {
			if d := s.opt.DescribeContext(ctx, event.MessageContext{ProjectID: thread.ProjectID}); d != "" {
				project = d
			}
		}
	}
	b := built{head: -1, lastUser: -1}
	var msgs []provider.Message
	if h := prompts.SegmentHead(seg.Profile, seg.Summary); h != "" {
		b.head = 0
		msgs = append(msgs, provider.Text(provider.RoleUser, h))
	}
	msgs = append(msgs, toProviderWith(s.opt.Attachments, withoutDMs(s.topicLabels(ctx, clipAnswered(history))), func(c event.MessageContext) string {
		if s.opt.DescribeContext == nil {
			return ""
		}
		return s.opt.DescribeContext(ctx, c)
	})...)
	for i := len(msgs) - 1; i > b.head; i-- {
		if msgs[i].Role == provider.RoleUser {
			b.lastUser = i
			break
		}
	}
	if b.lastUser >= 0 {
		changed, gone := profileDiff(seg.Profile, s.profile(ctx))
		tail := prompts.Tail(prompts.TurnEnv{
			Now:      s.opt.Now(),
			Machines: s.environment(ctx, thread.ID),
			Memories: s.recall(ctx, history, seg.Profile, changed), ProfileChanged: changed, ProfileGone: gone,
			Topic: s.topicEnv(ctx, tool.TurnFrom(ctx).TopicID),
			DMs:   s.dmLines(ctx, seg.CoversUpTo, history),
		})
		m := msgs[b.lastUser]
		parts := append(append([]provider.Part{}, m.Parts...), provider.Part{Text: "\n\n" + tail})
		msgs[b.lastUser] = provider.Message{Role: m.Role, Parts: parts}
	}
	b.req = provider.Request{
		Model: s.opt.Model, SessionID: thread.ID,
		System: prompts.Supervisor(prompts.Env{
			ThreadTitle: thread.Title, ThreadKind: thread.Kind, Project: project,
		}),
		Messages: msgs,
		Tools:    append(append(append(s.opt.Tools.Specs(), silentSpec), topicSpecs...), meetingProposeSpec),
	}
	if isMeeting(thread) {
		b.req.System = prompts.Meeting(prompts.MeetingEnv{Title: thread.Title, Project: project, Brief: thread.Brief})
		b.req.Tools = append(s.opt.Tools.Subset(meetingTools...).Specs(), silentSpec)
	}
	b.cacheAt()
	return b
}

// cacheAt marks where providers with explicit caching (Anthropic) should
// cache: after the segment head, before the turn's newest message (where
// the previous turn's cache ends), and at the end (for the turn's steps).
func (b *built) cacheAt() {
	var at []int
	add := func(i int) {
		if i < 0 || i >= len(b.req.Messages) {
			return
		}
		for _, x := range at {
			if x == i {
				return
			}
		}
		at = append(at, i)
	}
	add(b.head)
	add(b.lastUser - 1)
	add(len(b.req.Messages) - 1)
	b.req.CacheAt = at
}

// trim leaves out the oldest messages until the request fits limit tokens;
// the unanswered messages always stay.
func (s *Supervisor) trim(ctx context.Context, thread store.Thread, seg store.Segment, history []store.Message, limit int) ([]store.Message, built) {
	b := s.build(ctx, thread, seg, history)
	over := estimateRequest(b.req) - limit
	if over <= 0 {
		return history, b
	}
	upTo := oldestPending(history)
	drop := 0
	for drop < len(history) && over > 0 {
		m := history[drop]
		if m.Role == "user" && m.Seq >= upTo {
			break
		}
		over -= estimateText(plain(clip(m)).Content) + 4
		drop++
	}
	history = history[drop:]
	return history, s.build(ctx, thread, seg, history)
}

// isOverflow reports whether err says the request was longer than the
// model's context window.
func isOverflow(err error) bool {
	var api *provider.APIError
	if !errors.As(err, &api) {
		return false
	}
	if api.Status == 413 {
		return true
	}
	if api.Status != 400 {
		return false
	}
	body := strings.ToLower(api.Body)
	if !strings.Contains(body, "context") && !strings.Contains(body, "token") && !strings.Contains(body, "too long") {
		return false
	}
	for _, w := range []string{"length", "too long", "maximum", "exceed", "limit", "too many"} {
		if strings.Contains(body, w) {
			return true
		}
	}
	return false
}

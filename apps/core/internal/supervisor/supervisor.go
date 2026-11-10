// Package supervisor runs the supervisor loop: for each thread, user messages
// are handled one turn at a time; messages that arrive during a turn are
// folded into the next turn.
package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/attach"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

const (
	// MaxSteps bounds the model calls of one turn, so a tool loop cannot run
	// away. The last step is asked to report instead of calling tools.
	MaxSteps = 50
	// maxSameCall is how often one turn may make the very same tool call
	// (name and arguments); later copies are refused, not run.
	maxSameCall = 2
	// MaxAutoTurns bounds the turns a thread runs on triggers alone (task
	// news, follow-ups) since the user last wrote: past it the supervisor
	// stops carrying on by itself and tells the user once (decision 0025).
	MaxAutoTurns = 8
	// DefaultTaskBatch is how long task news waits for more before the
	// supervisor takes it up, so a burst is one turn.
	DefaultTaskBatch = 20 * time.Second
	// DefaultGather is how long the Hub lets an idle thread wait for more
	// quick messages before a turn (Options.Gather).
	DefaultGather = 1500 * time.Millisecond
)

// wrapUpNote is added to the results before the last step of a turn.
const wrapUpNote = "\n\n【sous】这一轮调用工具的次数快到上限了。不要再调用工具：用几句话告诉用户做到了哪一步、还剩什么；用户说“继续”你就接着做。"

type Options struct {
	Log      *eventlog.Log
	Provider provider.Provider
	Model    string
	Tools    *tool.Registry
	Now      func() time.Time
	// Environment adds lines about the world, such as the user's machines,
	// projects and the thread's open tasks, to every turn's prompt.
	Environment func(ctx context.Context, threadID string) []string
	// DescribeContext names what the user was looking at on the workbench
	// when they wrote a message (M5), such as "项目 sous" or "任务「…」".
	DescribeContext func(ctx context.Context, c event.MessageContext) string
	// Compactor folds old messages into a thread's summary
	// (long-main-thread); CompactorModel names it for usage. Without one,
	// threads are only trimmed to fit.
	Compactor      provider.Provider
	CompactorModel string
	// ContextWindow is the supervisor model's context window in tokens;
	// nil or 0 means DefaultContextWindow.
	ContextWindow func(ctx context.Context) int
	// Attachments holds the files users send; without it the model only
	// hears that a file was attached.
	Attachments *attach.Store
	// TaskBatch is how long task triggers wait before a turn; 0 means
	// DefaultTaskBatch, negative means no wait.
	TaskBatch time.Duration
	// Gather is how long an idle thread waits after a user message before
	// its turn starts, so a few quick messages are one turn (chat-topics);
	// 0 means no wait. A thread in a turn takes new messages in at once.
	Gather time.Duration
}

type Supervisor struct {
	opt Options
	ctx context.Context
	wg  sync.WaitGroup

	mu       sync.Mutex
	queues   map[string]chan struct{}
	ops      map[string][]func() // run by a thread's worker after its turn
	failures map[string]int      // failed compactions in a row, by thread
	batches  map[string]bool     // threads with task news waiting to be taken up
	gathers  map[string]bool     // idle threads waiting for more user messages
}

func New(opt Options) *Supervisor {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Tools == nil {
		opt.Tools = tool.NewRegistry()
	}
	if opt.TaskBatch == 0 {
		opt.TaskBatch = DefaultTaskBatch
	}
	return &Supervisor{opt: opt, queues: map[string]chan struct{}{}, ops: map[string][]func(){}, failures: map[string]int{}, batches: map[string]bool{}, gathers: map[string]bool{}}
}

// Start subscribes to stored events. Turns run until ctx is cancelled; Wait
// blocks until running turns have stopped.
func (s *Supervisor) Start(ctx context.Context) {
	s.ctx = ctx
	s.opt.Log.Listen(func(ev event.Event) {
		// A trigger is input nobody typed: a schedule came due, or the Hub
		// wants something followed up. It is stored as a user message.
		if (ev.Type == event.MessageUser || ev.Type == event.TriggerFired) && ev.ThreadID != "" {
			if ev.Type == event.TriggerFired && s.opt.TaskBatch > 0 {
				var p event.TriggerPayload
				if ev.Decode(&p) == nil && p.Kind == event.TriggerTask {
					s.later(ev.ThreadID)
					return
				}
			}
			if ev.Type == event.MessageUser && s.opt.Gather > 0 {
				s.gather(ev.ThreadID)
				return
			}
			s.signal(ev.ThreadID)
		}
		if ev.Type == event.MessageMoved && ev.ThreadID != "" {
			// A message taken back out of a topic waits for its own turn.
			s.signal(ev.ThreadID)
		}
	})
}

// later signals a thread after TaskBatch, once for all the task news that
// arrives meanwhile. A user message in between starts the turn earlier,
// and that turn sees the news too.
func (s *Supervisor) later(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.batches[threadID] {
		return
	}
	s.batches[threadID] = true
	time.AfterFunc(s.opt.TaskBatch, func() {
		s.mu.Lock()
		delete(s.batches, threadID)
		s.mu.Unlock()
		if s.ctx.Err() == nil {
			s.signal(threadID)
		}
	})
}

// gather starts an idle thread's turn after Gather, so messages sent in
// quick succession are one turn. A thread in a turn is signalled at once:
// the turn takes the message in, or the next one answers it.
func (s *Supervisor) gather(threadID string) {
	s.mu.Lock()
	_, busy := s.queues[threadID]
	if busy {
		s.mu.Unlock()
		s.signal(threadID)
		return
	}
	if s.gathers[threadID] {
		s.mu.Unlock()
		return
	}
	s.gathers[threadID] = true
	s.mu.Unlock()
	time.AfterFunc(s.opt.Gather, func() {
		s.mu.Lock()
		delete(s.gathers, threadID)
		s.mu.Unlock()
		if s.ctx.Err() == nil {
			s.signal(threadID)
		}
	})
}

func (s *Supervisor) Wait() { s.wg.Wait() }

// signal marks a thread as having new input and starts its worker if idle.
func (s *Supervisor) signal(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.queues[threadID]; ok {
		select {
		case ch <- struct{}{}:
		default: // a turn is already pending; it will see this message
		}
		return
	}
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	s.queues[threadID] = ch
	s.wg.Add(1)
	go s.worker(threadID, ch)
}

func (s *Supervisor) worker(threadID string, ch chan struct{}) {
	defer s.wg.Done()
	for {
		select {
		case <-ch:
			if s.turn(threadID) {
				// Another topic is waiting: it gets the next turn.
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			s.runOps(threadID)
			if s.ctx.Err() == nil {
				s.maybeCompact(threadID)
			}
		case <-s.ctx.Done():
			return
		default:
			s.mu.Lock()
			if len(ch) > 0 {
				s.mu.Unlock()
				continue
			}
			delete(s.queues, threadID)
			s.mu.Unlock()
			return
		}
	}
}

// enqueue runs op in the thread's worker, after any turn in progress.
func (s *Supervisor) enqueue(threadID string, op func()) {
	s.mu.Lock()
	s.ops[threadID] = append(s.ops[threadID], op)
	s.mu.Unlock()
	s.signal(threadID)
}

func (s *Supervisor) runOps(threadID string) {
	for {
		s.mu.Lock()
		ops := s.ops[threadID]
		delete(s.ops, threadID)
		s.mu.Unlock()
		if len(ops) == 0 {
			return
		}
		for _, op := range ops {
			if s.ctx.Err() != nil {
				return
			}
			op()
		}
	}
}

func newID() string { return ids.New() }

// turnState is what a turn keeps track of while it runs.
type turnState struct {
	thread string
	topic  string
	// upTo is the newest user message the turn answers; follow-ups taken
	// in move it on.
	upTo int64
	// cursor is the newest message the turn has looked at.
	cursor int64
	// attachable are messages that started a topic of their own during the
	// turn and may be follow-ups (topic_attach), by id.
	attachable map[string]int64
}

// turn answers the topic that has waited longest, and reports whether
// another one is waiting now.
func (s *Supervisor) turn(threadID string) bool {
	before, ok := s.answerTopic(threadID)
	if !ok || s.ctx.Err() != nil {
		return false
	}
	seg, err := s.opt.Log.Store.LatestSegment(s.ctx, threadID)
	if err != nil {
		return false
	}
	history, err := s.opt.Log.Store.MessagesAfter(s.ctx, threadID, seg.CoversUpTo)
	if err != nil {
		return false
	}
	next := pendingTopics(history)
	// Only go on when something changed, so input a turn cannot answer
	// does not spin.
	return len(next) > 0 && (next[0].ID != before.ID || next[0].Oldest != before.Oldest || next[0].Newest != before.Newest)
}

// answerTopic runs one turn and returns the topic it took up.
func (s *Supervisor) answerTopic(threadID string) (pendingTopic, bool) {
	ctx := s.ctx
	st := s.opt.Log.Store
	thread, err := st.Thread(ctx, threadID)
	if err != nil {
		slog.Error("supervisor: load thread", "thread", threadID, "err", err)
		return pendingTopic{}, false
	}
	seg, err := s.segment(ctx, threadID)
	if err != nil {
		slog.Error("supervisor: load segment", "thread", threadID, "err", err)
		return pendingTopic{}, false
	}
	all, err := st.MessagesAfter(ctx, threadID, seg.CoversUpTo)
	if err != nil {
		slog.Error("supervisor: load messages", "thread", threadID, "err", err)
		return pendingTopic{}, false
	}
	waiting := pendingTopics(all)
	if len(waiting) == 0 {
		return pendingTopic{}, false // every user message already has a reply
	}
	pick := waiting[0]
	tr := &turnState{thread: threadID, topic: pick.ID, upTo: pick.Newest, attachable: map[string]int64{}}
	for _, m := range all {
		tr.cursor = max(tr.cursor, m.Seq)
	}
	history := forTopic(all, tr.topic)
	if streak, auto := autoStreak(all, tr.topic); auto && streak >= MaxAutoTurns {
		// Carrying on by itself this long without the user: stop here, so
		// tasks and triggers cannot keep each other going (decision 0025).
		s.reply(threadID, tr.topic, ids.New(), tr.upTo, event.KindSilent, fmt.Sprintf("没有用户参与时已经连续自动推进了 %d 轮，暂停自动推进", streak), nil)
		if streak == MaxAutoTurns {
			s.notice(ctx, threadID, tr.topic, fmt.Sprintf("我已经连续自动推进了 %d 轮（任务的进展和跟进），先停下来，等你看一下再继续。", streak))
		}
		return pick, true
	}
	s.seen(threadID, pick.MsgIDs)

	mainID, _ := st.MainThreadID(ctx)
	turnCtx := func() context.Context {
		return tool.WithTurn(s.ctx, tool.Turn{ThreadID: threadID, MainThreadID: mainID, ProjectID: thread.ProjectID, ReplyTo: tr.upTo, TopicID: tr.topic})
	}
	ctx = turnCtx()
	// load rebuilds the request from the current segment, compacting
	// first (reason) when it would not fit, and leaving out the oldest
	// messages when it still does not.
	hard := int(HardRatio * float64(s.window(ctx)))
	load := func(reason string) provider.Request {
		if reason != "" && s.opt.Compactor != nil {
			if err := s.compact(s.ctx, threadID, reason); err != nil {
				slog.Warn("supervisor: compaction", "thread", threadID, "reason", reason, "err", err)
			}
			if g, err := s.segment(ctx, threadID); err == nil {
				if h, err := st.MessagesAfter(ctx, threadID, g.CoversUpTo); err == nil {
					seg, history = g, forTopic(h, tr.topic)
				}
			}
		}
		_, b := s.trim(ctx, thread, seg, history, hard)
		return b.req
	}
	req := s.build(ctx, thread, seg, history).req
	if estimateRequest(req) > hard {
		req = load(event.CompactHard)
	}
	if tr.topic != "" {
		offer, ids := offerFresh(all, tr, pick)
		addNote(&req, offer)
		s.seen(threadID, ids)
	}
	retried := false
	messageID := ids.New()
	// The tools called are shown under the turn's last stored message
	// (anchor); calls made before anything was stored wait in pending and
	// go with the next message.
	var anchor string
	var anchorTools, pending []event.ToolNote
	reply := func(text string, err error) { s.store(threadID, tr.topic, messageID, tr.upTo, "", text, err, pending) }
	var texts []string // said since the last stored step
	spoke := false     // said anything this turn
	emit := func(d provider.Delta) {
		if d.Text == "" && d.Thinking == "" {
			return
		}
		s.opt.Log.Emit(event.New(event.MessageAssistantDelta, event.ActorSupervisor, threadID,
			event.DeltaPayload{MessageID: messageID, Delta: d.Text + d.Thinking, Thinking: d.Text == ""}))
	}
	// takeIn adds what the user wrote meanwhile to the end of the request.
	takeIn := func() string {
		in := s.absorb(ctx, tr)
		s.seen(threadID, in.seen)
		return in.note
	}

	seen := map[string]int{} // tool calls made this turn
	verifier := claimVerifier{s: s, receipts: map[string]taskReceipt{}, threadID: threadID, replyTo: tr.upTo}
	checkedDispatch := false
	for step := 0; step < MaxSteps; step++ {
		verifier.step = step
		if step > 0 {
			addNote(&req, takeIn())
		}
		if len(texts) > 0 {
			emit(provider.Delta{Text: "\n\n"})
		}
		// Hold every text step until its IDs and state claims are checked.
		// Thinking still streams; a previous receipt never bypasses the guard.
		stream := func(d provider.Delta) {
			if d.Text == "" {
				emit(d)
			}
		}
		resp, err := s.opt.Provider.Stream(ctx, req, stream)
		if err != nil && step == 0 && !retried && isOverflow(err) {
			retried = true
			slog.Warn("supervisor: context overflow, compacting", "thread", threadID, "err", err)
			req = load(event.CompactOverflow)
			step--
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return pick, false
			}
			slog.Warn("supervisor: model call failed", "thread", threadID, "err", err)
			reply(strings.Join(texts, "\n\n"), err)
			return pick, true
		}
		s.record(threadID, "supervisor", provider.ModelOf(resp, s.opt.Model), s.opt.Provider, resp)
		calls := resp.Message.ToolCalls()
		t := resp.Message.PlainText()
		if claimsDispatch(t) && len(taskIDPattern.FindAllString(t, -1)) == 0 {
			if len(calls) == 0 && !checkedDispatch && step < MaxSteps-1 {
				checkedDispatch = true
				slog.Warn("supervisor: unverified dispatch claim", "thread", threadID, "task_id", "", "verdict", "missing_id")
				req.Messages = append(req.Messages, resp.Message, provider.Text(provider.RoleUser, dispatchCheckNote))
				continue
			}
			if len(calls) > 0 {
				t = ""
			} // wait for the result before reporting
		}
		t = verifier.check(ctx, t)
		if t != resp.Message.PlainText() {
			// Subsequent model steps see the same corrected words as the user.
			parts := append([]provider.Part(nil), resp.Message.Parts...)
			replacement := t
			for i := range parts {
				if parts[i].Text != "" {
					parts[i].Text, replacement = replacement, ""
				}
			}
			resp.Message.Parts = parts
		}

		if t != "" {
			emit(provider.Delta{Text: t})
			texts = append(texts, t)
			spoke = true
		}
		if len(calls) == 0 {
			reply(strings.Join(texts, "\n\n"), nil)
			return pick, true
		}
		if reason, ok := silentOnly(calls); ok && !spoke {
			s.reply(threadID, tr.topic, messageID, tr.upTo, event.KindSilent, reason, nil)
			return pick, true
		}
		if len(texts) > 0 {
			// Store what was said before the tools run, so their results
			// (task chips, reports) come after it, and stream the rest of
			// the turn as a new message.
			s.store(threadID, tr.topic, messageID, tr.upTo, event.KindStep, strings.Join(texts, "\n\n"), nil, pending)
			anchor, anchorTools, pending = messageID, pending, nil
			messageID, texts = ids.New(), nil
		}
		if step == MaxSteps-1 {
			break // the last step was asked to report and still called tools
		}
		// The user wrote while the model decided: anything that changes
		// something waits until the model has read it (R4).
		note := takeIn()
		hold := note != "" && hasSideEffects(calls)
		ctx = turnCtx()
		req.Messages = append(req.Messages, resp.Message)
		results := provider.Message{Role: provider.RoleTool}
		var notes []event.ToolNote
		for _, c := range calls {
			var r provider.ToolResult
			key := c.Name + "\x00" + string(c.Args)
			if !hold {
				seen[key]++
			}
			switch {
			case hold:
				r = provider.ToolResult{CallID: c.ID, Content: heldNote, IsError: true}
			case c.Name == silentSpec.Name:
				r = provider.ToolResult{CallID: c.ID, Content: "stay_silent 只能单独调用；本轮已经发言，请直接回复。", IsError: true}
			case seen[key] > maxSameCall:
				r = provider.ToolResult{CallID: c.ID, IsError: true,
					Content: "这一轮已经用完全相同的参数调用过它了，结果不会变。换个办法，或者根据已有的结果回答。"}
			case isTopicTool(c.Name):
				r = s.topicCall(ctx, tr, c)
				ctx = turnCtx() // an attached message moves upTo on
			default:
				r = s.opt.Tools.Call(ctx, c)
			}
			verifier.collect(ctx, r)
			results.Parts = append(results.Parts, provider.Part{ToolResult: &r})
			notes = append(notes, toolNote(c, r))
		}
		if anchor != "" {
			anchorTools = append(anchorTools, notes...)
			s.tools(threadID, anchor, anchorTools)
		} else {
			pending = append(pending, notes...)
		}
		if step == MaxSteps-2 && len(results.Parts) > 0 {
			last := results.Parts[len(results.Parts)-1].ToolResult
			last.Content += wrapUpNote
		}
		req.Messages = append(req.Messages, results)
		addNote(&req, note)
		req.CacheAt = append(req.CacheAt[:0:0], req.CacheAt...)
		if n := len(req.CacheAt); n > 0 {
			req.CacheAt[n-1] = len(req.Messages) - 1 // the end moves with the steps
		}
	}
	reply(strings.Join(texts, "\n\n")+"\n\n（这一轮调用工具的次数达到上限，已停止。）", nil)
	return pick, true
}

func (s *Supervisor) reply(threadID, topic, messageID string, replyTo int64, kind, text string, callErr error) {
	s.store(threadID, topic, messageID, replyTo, kind, text, callErr, nil)
}

// tools records the supervisor's tool calls shown under a stored message.
func (s *Supervisor) tools(threadID, messageID string, notes []event.ToolNote) {
	ev := event.New(event.MessageTools, event.ActorSupervisor, threadID, event.MessageToolsPayload{MessageID: messageID, Tools: notes})
	if _, err := s.opt.Log.Append(s.ctx, ev); err != nil {
		slog.Error("supervisor: store tool calls", "thread", threadID, "err", err)
	}
}

// store is reply that also shows tool calls under the message.
func (s *Supervisor) store(threadID, topic, messageID string, replyTo int64, kind, text string, callErr error, tools []event.ToolNote) {
	p := event.MessagePayload{MessageID: messageID, Content: text, ReplyTo: replyTo, Kind: kind, Tools: tools, TopicID: topic}
	if callErr != nil {
		p.Error = true
		if p.Content != "" {
			p.Content += "\n\n"
		}
		p.Content += "（模型调用失败：" + callErr.Error() + "）"
	}
	if strings.TrimSpace(p.Content) == "" && kind != event.KindSilent {
		if kind == "" && replyTo > 0 && s.hasStep(threadID, replyTo) {
			// Earlier steps of this turn spoke; the turn just ends.
			kind = event.KindSilent
			p.Kind = kind
		} else {
			p.Content = "（模型没有返回内容）"
		}
	}
	if _, err := s.opt.Log.Append(s.ctx, event.New(event.MessageAssistant, event.ActorSupervisor, threadID, p)); err != nil {
		slog.Error("supervisor: store reply", "thread", threadID, "err", err)
	}
}

// autoStreak counts the turns answered since the user last wrote, and
// reports whether all the input waiting in topic is triggers.
func autoStreak(msgs []store.Message, topic string) (int, bool) {
	var human int64
	for _, m := range msgs {
		if m.Role == "user" && m.Kind != event.KindTrigger {
			human = m.Seq
		}
	}
	waiting := false // the user wrote in topic and is still waiting
	answered := answers(msgs)
	for _, m := range msgs {
		if m.Role == "user" && m.Kind != event.KindTrigger && m.TopicID == topic && m.Seq > answered[topic] {
			waiting = true
		}
	}
	turns := map[int64]bool{}
	for _, m := range msgs {
		if m.Role == "assistant" && m.ReplyTo > 0 && m.Kind != event.KindStep && m.Kind != event.KindConsult && m.ReplyTo > human {
			turns[m.ReplyTo] = true
		}
	}
	return len(turns), !waiting
}

// notice posts a note to the main thread that pushes to the user's devices.
// It goes into the turn's topic when the turn is the main thread's.
func (s *Supervisor) notice(ctx context.Context, threadID, topic, text string) {
	main, err := s.opt.Log.Store.MainThreadID(ctx)
	if err != nil {
		return
	}
	if threadID != main {
		topic = ""
	}
	ev := event.New(event.MessageAssistant, event.ActorSupervisor, main,
		event.MessagePayload{MessageID: ids.New(), Content: text, Kind: event.KindNotice, Notify: true, TopicID: topic})
	if _, err := s.opt.Log.Append(ctx, ev); err != nil {
		slog.Error("supervisor: notice", "err", err)
	}
}

// hasStep reports whether the turn answering replyTo stored a step (or a
// consultant's answer).
func (s *Supervisor) hasStep(threadID string, replyTo int64) bool {
	msgs, err := s.opt.Log.Store.MessagesAfter(s.ctx, threadID, replyTo-1)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		// A consultant's answer speaks for the turn as well: the supervisor
		// may end it without words of its own.
		if m.Role == "assistant" && (m.Kind == event.KindStep || m.Kind == event.KindConsult) && m.ReplyTo == replyTo {
			return true
		}
	}
	return false
}

// toProvider converts stored messages to model messages. Each reply is placed
// right after the user message it answered, so messages that arrived during a
// turn come after its reply; the steps and the last message of one turn are
// joined into one assistant message. Failed replies are left out so the model
// does not imitate error text.
func toProvider(msgs []store.Message, label ...func(event.MessageContext) string) []provider.Message {
	return toProviderWith(nil, msgs, label...)
}

// toProviderWith is toProvider that also shows the files of user messages:
// pictures as image parts, text files inline, anything else by name.
func toProviderWith(files *attach.Store, msgs []store.Message, label ...func(event.MessageContext) string) []provider.Message {
	replies := map[int64][]store.Message{}
	for _, m := range msgs {
		if m.Role == "assistant" && m.ReplyTo > 0 {
			replies[m.ReplyTo] = append(replies[m.ReplyTo], m)
		}
	}
	out := make([]provider.Message, 0, len(msgs))
	add := func(m store.Message) {
		if (m.Error || m.Kind == event.KindSilent) && len(m.Tools) == 0 {
			return
		}
		role := provider.RoleUser
		if m.Role == "assistant" {
			role = provider.RoleAssistant
		}
		content := historyText(m)
		if m.Role == "user" && m.Context != nil && len(label) > 0 && label[0] != nil {
			if l := label[0](*m.Context); l != "" {
				content = "【用户写这条消息时，正在工作台上看：" + l + "。消息里的“这个”“它”多半指它】\n" + content
			}
		}
		if m.Role == "user" && m.Context != nil && len(m.Context.Mentions) > 0 {
			content = mentionNote(m.Context.Mentions, label...) + "\n" + content
		}
		msg := provider.Text(role, content)
		if m.Role == "user" {
			msg.Parts = append(msg.Parts, attachmentParts(files, m.Attachments)...)
		}
		out = append(out, msg)
	}
	for _, m := range msgs {
		if m.Role == "assistant" && m.ReplyTo > 0 {
			continue // placed after its user message
		}
		add(m)
		if m.Role == "user" {
			var parts []string
			for _, r := range replies[m.Seq] {
				if (!r.Error && r.Kind != event.KindSilent && strings.TrimSpace(r.Content) != "") || len(r.Tools) > 0 {
					parts = append(parts, consultLabel(r)+historyText(r))
				}
			}
			if len(parts) > 0 {
				out = append(out, provider.Text(provider.RoleAssistant, strings.Join(parts, "\n\n")))
			}
		}
	}
	return out
}

// mentionNote tells the model which projects the @ references in a user
// message stand for, by id, so it does not have to guess from the name.
func mentionNote(ms []event.Mention, label ...func(event.MessageContext) string) string {
	names := make([]string, 0, len(ms))
	for _, mn := range ms {
		d := ""
		if len(label) > 0 && label[0] != nil {
			d = label[0](event.MessageContext{ProjectID: mn.ProjectID})
		}
		if d == "" {
			d = fmt.Sprintf("项目 %s（id %s）", mn.Name, mn.ProjectID)
		}
		names = append(names, "@"+mn.Name+" = "+d)
	}
	return "【消息里用 @ 引用了工作台上的项目：" + strings.Join(names, "；") + "】"
}

// consultLabel marks a project consultant's answer, which the user saw as
// it is, in the supervisor's view of the conversation.
func consultLabel(m store.Message) string {
	if m.Kind != event.KindConsult {
		return ""
	}
	who := "项目顾问"
	if m.Source != nil && m.Source.Agent != "" {
		who += "（" + m.Source.Agent + "）"
	}
	return "【" + who + "的回答，用户已经看到原文】\n"
}

// silentSpec lets the supervisor end a turn without speaking.
var silentSpec = provider.ToolSpec{
	Name: "stay_silent",
	Description: "这条消息不需要回复时调用（比如用户只是确认、道谢，或者明确说只记录不用回答）。" +
		"必须单独调用，并且本轮不能再输出文字。",
	Schema: json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"为什么不回复，一句话"}}}`),
}

// silentOnly reports whether calls is a lone stay_silent call, and its reason.
func silentOnly(calls []provider.ToolCall) (string, bool) {
	if len(calls) != 1 || calls[0].Name != silentSpec.Name {
		return "", false
	}
	var a struct{ Reason string }
	json.Unmarshal(calls[0].Args, &a)
	return strings.TrimSpace(a.Reason), true
}

// RecallLimit is how many memories related to the conversation a turn sees,
// besides the profile memories the segment already shows.
const RecallLimit = 6

// recall picks the memories related to the latest user messages, leaving
// out the ones the segment head or the tail already show.
func (s *Supervisor) recall(ctx context.Context, history []store.Message, shown ...[]string) []string {
	var query []string
	for i := len(history) - 1; i >= 0 && len(query) < 3; i-- {
		if history[i].Role == "user" {
			query = append(query, history[i].Content)
		}
	}
	related, err := s.opt.Log.Store.SearchMemories(ctx, strings.Join(query, " "), RecallLimit)
	if err != nil {
		slog.Warn("supervisor: search memories", "err", err)
	}
	seen := map[string]bool{}
	for _, list := range shown {
		for _, m := range list {
			seen[memoryID(m)] = true
		}
	}
	var out []string
	for _, m := range related {
		if !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, tool.FormatMemory(m))
		}
	}
	return out
}

func (s *Supervisor) environment(ctx context.Context, threadID string) []string {
	if s.opt.Environment == nil {
		return nil
	}
	return s.opt.Environment(ctx, threadID)
}

// attachmentParts shows a message's files to the model.
func attachmentParts(files *attach.Store, atts []event.Attachment) []provider.Part {
	var parts []provider.Part
	for _, a := range atts {
		var data []byte
		if files != nil {
			var err error
			if data, err = files.Read(a); err != nil {
				parts = append(parts, provider.Part{Text: fmt.Sprintf("\n【附件 %s 已经找不到了】", a.Name)})
				continue
			}
		}
		switch {
		case data == nil:
			parts = append(parts, provider.Part{Text: fmt.Sprintf("\n【用户附了文件 %s】", a.Name)})
		case attach.IsImage(a.Mime):
			parts = append(parts, provider.Part{Text: fmt.Sprintf("\n【图片 %s】", a.Name)}, provider.Part{Image: &provider.Image{Mime: a.Mime, Data: data}})
		case attach.IsText(a.Mime):
			note := ""
			if len(data) > attach.MaxTextBytes {
				data = data[:attach.MaxTextBytes]
				note = fmt.Sprintf("\n【文件太长，只给出前 %d KB】", attach.MaxTextBytes>>10)
			}
			parts = append(parts, provider.Part{Text: fmt.Sprintf("\n【文件 %s】\n%s%s\n【文件结束】", a.Name, strings.ToValidUTF8(string(data), "�"), note)})
		default:
			parts = append(parts, provider.Part{Text: fmt.Sprintf("\n【附件 %s（%s，%d KB）：这种文件读不了，只能告诉用户你看不到它的内容】", a.Name, a.Mime, a.Size>>10)})
		}
	}
	return parts
}

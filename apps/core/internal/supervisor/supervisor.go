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
	// MaxSteps bounds the model calls of one turn, so a tool loop cannot run away.
	MaxSteps = 12
)

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
}

type Supervisor struct {
	opt Options
	ctx context.Context
	wg  sync.WaitGroup

	mu       sync.Mutex
	queues   map[string]chan struct{}
	ops      map[string][]func() // run by a thread's worker after its turn
	failures map[string]int      // failed compactions in a row, by thread
}

func New(opt Options) *Supervisor {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Tools == nil {
		opt.Tools = tool.NewRegistry()
	}
	return &Supervisor{opt: opt, queues: map[string]chan struct{}{}, ops: map[string][]func(){}, failures: map[string]int{}}
}

// Start subscribes to stored events. Turns run until ctx is cancelled; Wait
// blocks until running turns have stopped.
func (s *Supervisor) Start(ctx context.Context) {
	s.ctx = ctx
	s.opt.Log.Listen(func(ev event.Event) {
		// A trigger is input nobody typed: a schedule came due, or the Hub
		// wants something followed up. It is stored as a user message.
		if (ev.Type == event.MessageUser || ev.Type == event.TriggerFired) && ev.ThreadID != "" {
			s.signal(ev.ThreadID)
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
			s.turn(threadID)
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

func (s *Supervisor) turn(threadID string) {
	ctx := s.ctx
	st := s.opt.Log.Store
	thread, err := st.Thread(ctx, threadID)
	if err != nil {
		slog.Error("supervisor: load thread", "thread", threadID, "err", err)
		return
	}
	seg, err := s.segment(ctx, threadID)
	if err != nil {
		slog.Error("supervisor: load segment", "thread", threadID, "err", err)
		return
	}
	history, err := st.MessagesAfter(ctx, threadID, seg.CoversUpTo)
	if err != nil {
		slog.Error("supervisor: load messages", "thread", threadID, "err", err)
		return
	}
	upTo := unanswered(history)
	if upTo == 0 {
		return // every user message already has a reply
	}

	mainID, _ := st.MainThreadID(ctx)
	ctx = tool.WithTurn(ctx, tool.Turn{ThreadID: threadID, MainThreadID: mainID, ProjectID: thread.ProjectID})
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
					seg, history = g, h
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
	retried := false
	messageID := ids.New()
	reply := func(text string, err error) { s.reply(threadID, messageID, upTo, "", text, err) }
	var texts []string // said since the last stored step
	spoke := false     // said anything this turn
	emit := func(d provider.Delta) {
		if d.Text == "" && d.Thinking == "" {
			return
		}
		s.opt.Log.Emit(event.New(event.MessageAssistantDelta, event.ActorSupervisor, threadID,
			event.DeltaPayload{MessageID: messageID, Delta: d.Text + d.Thinking, Thinking: d.Text == ""}))
	}

	for step := 0; step < MaxSteps; step++ {
		if len(texts) > 0 {
			emit(provider.Delta{Text: "\n\n"})
		}
		resp, err := s.opt.Provider.Stream(ctx, req, emit)
		if err != nil && step == 0 && !retried && isOverflow(err) {
			retried = true
			slog.Warn("supervisor: context overflow, compacting", "thread", threadID, "err", err)
			req = load(event.CompactOverflow)
			step--
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("supervisor: model call failed", "thread", threadID, "err", err)
			reply(strings.Join(texts, "\n\n"), err)
			return
		}
		s.record(threadID, "supervisor", provider.ModelOf(resp, s.opt.Model), s.opt.Provider, resp)
		if t := resp.Message.PlainText(); t != "" {
			texts = append(texts, t)
			spoke = true
		}
		calls := resp.Message.ToolCalls()
		if len(calls) == 0 {
			reply(strings.Join(texts, "\n\n"), nil)
			return
		}
		if reason, ok := silentOnly(calls); ok && !spoke {
			s.reply(threadID, messageID, upTo, event.KindSilent, reason, nil)
			return
		}
		if len(texts) > 0 {
			// Store what was said before the tools run, so their results
			// (task chips, reports) come after it, and stream the rest of
			// the turn as a new message.
			s.reply(threadID, messageID, upTo, event.KindStep, strings.Join(texts, "\n\n"), nil)
			messageID, texts = ids.New(), nil
		}
		req.Messages = append(req.Messages, resp.Message)
		results := provider.Message{Role: provider.RoleTool}
		for _, c := range calls {
			var r provider.ToolResult
			if c.Name == silentSpec.Name {
				r = provider.ToolResult{CallID: c.ID, Content: "stay_silent 只能单独调用；本轮已经发言，请直接回复。", IsError: true}
			} else {
				r = s.opt.Tools.Call(ctx, c)
			}
			results.Parts = append(results.Parts, provider.Part{ToolResult: &r})
		}
		req.Messages = append(req.Messages, results)
		req.CacheAt = append(req.CacheAt[:0:0], req.CacheAt...)
		if n := len(req.CacheAt); n > 0 {
			req.CacheAt[n-1] = len(req.Messages) - 1 // the end moves with the steps
		}
	}
	reply(strings.Join(texts, "\n\n")+"\n\n（这一轮调用工具的次数达到上限，已停止。）", nil)
}

func (s *Supervisor) reply(threadID, messageID string, replyTo int64, kind, text string, callErr error) {
	p := event.MessagePayload{MessageID: messageID, Content: text, ReplyTo: replyTo, Kind: kind}
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

// hasStep reports whether the turn answering replyTo stored a step.
func (s *Supervisor) hasStep(threadID string, replyTo int64) bool {
	msgs, err := s.opt.Log.Store.MessagesAfter(s.ctx, threadID, replyTo-1)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if m.Role == "assistant" && m.Kind == event.KindStep && m.ReplyTo == replyTo {
			return true
		}
	}
	return false
}

// unanswered returns the seq of the newest user message that no assistant
// reply covers yet, or 0. Steps do not answer: a turn interrupted after a
// step is answered again.
func unanswered(msgs []store.Message) int64 {
	var answered, newest int64
	for _, m := range msgs {
		if m.Role == "assistant" && m.Kind != event.KindStep && m.ReplyTo > answered {
			answered = m.ReplyTo
		}
		if m.Role == "user" {
			newest = m.Seq
		}
	}
	if newest > answered {
		return newest
	}
	return 0
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
		if m.Error || m.Kind == event.KindSilent {
			return
		}
		role := provider.RoleUser
		if m.Role == "assistant" {
			role = provider.RoleAssistant
		}
		content := m.Content
		if m.Role == "user" && m.Context != nil && len(label) > 0 && label[0] != nil {
			if l := label[0](*m.Context); l != "" {
				content = "【用户写这条消息时，正在工作台上看：" + l + "。消息里的“这个”“它”多半指它】\n" + content
			}
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
				if !r.Error && r.Kind != event.KindSilent && strings.TrimSpace(r.Content) != "" {
					parts = append(parts, r.Content)
				}
			}
			if len(parts) > 0 {
				out = append(out, provider.Text(provider.RoleAssistant, strings.Join(parts, "\n\n")))
			}
		}
	}
	return out
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

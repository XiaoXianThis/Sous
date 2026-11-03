// Package supervisor runs the supervisor loop: for each thread, user messages
// are handled one turn at a time; messages that arrive during a turn are
// folded into the next turn.
package supervisor

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

const (
	// HistoryMessages is how many recent messages a turn sees.
	HistoryMessages = 40
	// MaxSteps bounds the model calls of one turn, so a tool loop cannot run away.
	MaxSteps = 8
)

type Options struct {
	Log      *eventlog.Log
	Provider provider.Provider
	Model    string
	Tools    *tool.Registry
	Now      func() time.Time
}

type Supervisor struct {
	opt Options
	ctx context.Context
	wg  sync.WaitGroup

	mu     sync.Mutex
	queues map[string]chan struct{}
}

func New(opt Options) *Supervisor {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Tools == nil {
		opt.Tools = tool.NewRegistry()
	}
	return &Supervisor{opt: opt, queues: map[string]chan struct{}{}}
}

// Start subscribes to stored events. Turns run until ctx is cancelled; Wait
// blocks until running turns have stopped.
func (s *Supervisor) Start(ctx context.Context) {
	s.ctx = ctx
	s.opt.Log.Listen(func(ev event.Event) {
		if ev.Type == event.MessageUser && ev.ThreadID != "" {
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

func (s *Supervisor) turn(threadID string) {
	ctx := s.ctx
	st := s.opt.Log.Store
	thread, err := st.Thread(ctx, threadID)
	if err != nil {
		slog.Error("supervisor: load thread", "thread", threadID, "err", err)
		return
	}
	history, err := st.Messages(ctx, threadID, 0, HistoryMessages)
	if err != nil {
		slog.Error("supervisor: load messages", "thread", threadID, "err", err)
		return
	}
	upTo := unanswered(history)
	if upTo == 0 {
		return // every user message already has a reply
	}

	mainID, _ := st.MainThreadID(ctx)
	ctx = tool.WithTurn(ctx, tool.Turn{ThreadID: threadID, MainThreadID: mainID})
	req := provider.Request{
		Model: s.opt.Model,
		System: prompts.Supervisor(prompts.Env{
			Now: s.opt.Now(), ThreadTitle: thread.Title, ThreadKind: thread.Kind,
			Memories: s.recall(ctx, history),
		}),
		Messages: toProvider(history),
		Tools:    append(s.opt.Tools.Specs(), silentSpec),
	}
	messageID := ids.New()
	reply := func(text string, err error) { s.reply(threadID, messageID, upTo, "", text, err) }
	var texts []string
	emit := func(d provider.Delta) {
		if d.Text == "" && d.Thinking == "" {
			return
		}
		s.opt.Log.Emit(event.New(event.MessageAssistantDelta, event.ActorSupervisor, threadID,
			event.DeltaPayload{MessageID: messageID, Delta: d.Text + d.Thinking, Thinking: d.Text == ""}))
	}

	for step := 0; step < MaxSteps; step++ {
		if step > 0 && len(texts) > 0 {
			emit(provider.Delta{Text: "\n\n"})
		}
		resp, err := s.opt.Provider.Stream(ctx, req, emit)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("supervisor: model call failed", "thread", threadID, "err", err)
			reply(strings.Join(texts, "\n\n"), err)
			return
		}
		s.recordUsage(threadID, resp.Usage)
		if t := resp.Message.PlainText(); t != "" {
			texts = append(texts, t)
		}
		calls := resp.Message.ToolCalls()
		if len(calls) == 0 {
			reply(strings.Join(texts, "\n\n"), nil)
			return
		}
		if reason, ok := silentOnly(calls); ok && len(texts) == 0 {
			s.reply(threadID, messageID, upTo, event.KindSilent, reason, nil)
			return
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
		p.Content = "（模型没有返回内容）"
	}
	if _, err := s.opt.Log.Append(s.ctx, event.New(event.MessageAssistant, event.ActorSupervisor, threadID, p)); err != nil {
		slog.Error("supervisor: store reply", "thread", threadID, "err", err)
	}
}

func (s *Supervisor) recordUsage(threadID string, u provider.Usage) {
	ev := event.New(event.UsageRecorded, event.ActorSupervisor, threadID, event.UsagePayload{
		Provider: s.opt.Provider.Name(), Model: s.opt.Model, Role: "supervisor",
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens,
	})
	if _, err := s.opt.Log.Append(s.ctx, ev); err != nil {
		slog.Error("supervisor: store usage", "err", err)
	}
}

// unanswered returns the seq of the newest user message that no assistant
// reply covers yet, or 0.
func unanswered(msgs []store.Message) int64 {
	var answered, newest int64
	for _, m := range msgs {
		if m.Role == "assistant" && m.ReplyTo > answered {
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
// turn come after its reply. Failed replies are left out so the model does not
// imitate error text.
func toProvider(msgs []store.Message) []provider.Message {
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
		out = append(out, provider.Text(role, m.Content))
	}
	for _, m := range msgs {
		if m.Role == "assistant" && m.ReplyTo > 0 {
			continue // placed after its user message
		}
		add(m)
		if m.Role == "user" {
			for _, r := range replies[m.Seq] {
				add(r)
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
// besides the user's profile memories.
const RecallLimit = 6

// recall picks the memories a turn should see: the user's profile, plus the
// ones related to the latest user messages.
func (s *Supervisor) recall(ctx context.Context, history []store.Message) []string {
	st := s.opt.Log.Store
	profile, err := st.Memories(ctx, "profile", 10)
	if err != nil {
		slog.Warn("supervisor: load profile memories", "err", err)
	}
	var query []string
	for i := len(history) - 1; i >= 0 && len(query) < 3; i-- {
		if history[i].Role == "user" {
			query = append(query, history[i].Content)
		}
	}
	related, err := st.SearchMemories(ctx, strings.Join(query, " "), RecallLimit)
	if err != nil {
		slog.Warn("supervisor: search memories", "err", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range append(profile, related...) {
		if !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, tool.FormatMemory(m))
		}
	}
	return out
}

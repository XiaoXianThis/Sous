package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Topics (work/specs/chat-topics). A thread's messages are grouped into
// topics; the supervisor answers one topic per turn, oldest waiting first,
// and keeps one context for the whole thread.

// pendingTopic is a topic with input no reply covers yet.
type pendingTopic struct {
	ID     string // "" for messages from before topics
	Oldest int64
	Newest int64
	MsgIDs []string
}

// answers is, by topic, the newest user message a reply covers: a reply
// (not a step or a consultant's answer) covers the messages of its topic up
// to its reply_to.
func answers(msgs []store.Message) map[string]int64 {
	topicAt := map[int64]string{}
	for _, m := range msgs {
		if m.Role == "user" {
			topicAt[m.Seq] = m.TopicID
		}
	}
	out := map[string]int64{}
	for _, m := range msgs {
		if m.Role != "assistant" || m.Kind == event.KindStep || m.Kind == event.KindConsult || m.ReplyTo == 0 {
			continue
		}
		topic := m.TopicID
		if t, ok := topicAt[m.ReplyTo]; ok && topic == "" {
			topic = t // a reply written without a topic answers the message it names
		}
		if m.ReplyTo > out[topic] {
			out[topic] = m.ReplyTo
		}
	}
	return out
}

// pendingTopics lists the topics with unanswered input, the one waiting
// longest first.
func pendingTopics(msgs []store.Message) []pendingTopic {
	answered := answers(msgs)
	idx := map[string]int{}
	var out []pendingTopic
	for _, m := range msgs {
		if m.Role != "user" || m.Seq <= answered[m.TopicID] {
			continue
		}
		i, ok := idx[m.TopicID]
		if !ok {
			i = len(out)
			idx[m.TopicID] = i
			out = append(out, pendingTopic{ID: m.TopicID, Oldest: m.Seq})
		}
		out[i].Newest = m.Seq
		out[i].MsgIDs = append(out[i].MsgIDs, m.ID)
	}
	return out
}

// unanswered returns the seq of the newest unanswered user message of the
// topic that has waited longest, or 0: what the next turn answers.
func unanswered(msgs []store.Message) int64 {
	if p := pendingTopics(msgs); len(p) > 0 {
		return p[0].Newest
	}
	return 0
}

// oldestPending is the seq of the oldest user message no reply covers, in
// any topic, or 0. Nothing from there on may be left out or folded away.
func oldestPending(msgs []store.Message) int64 {
	var oldest int64
	for _, p := range pendingTopics(msgs) {
		if oldest == 0 || p.Oldest < oldest {
			oldest = p.Oldest
		}
	}
	return oldest
}

// forTopic is the history a turn on topic sees: the input other topics are
// still waiting with is left for their own turns, so the model answers one
// topic and each reply follows its own messages.
func forTopic(msgs []store.Message, topic string) []store.Message {
	answered := answers(msgs)
	out := make([]store.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "user" && m.TopicID != topic && m.Seq > answered[m.TopicID] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// topicLabels tells the model which topic each user message is in, by the
// seq of the message that started it: titles change, the number does not,
// so the history stays the same from turn to turn.
func (s *Supervisor) topicLabels(ctx context.Context, msgs []store.Message) []store.Message {
	var ids []string
	seen := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "user" && m.TopicID != "" && !seen[m.TopicID] {
			seen[m.TopicID] = true
			ids = append(ids, m.TopicID)
		}
	}
	if len(ids) == 0 {
		return msgs
	}
	topics, err := s.opt.Log.Store.TopicsByID(ctx, ids)
	if err != nil || len(topics) == 0 {
		return msgs
	}
	out := make([]store.Message, len(msgs))
	for i, m := range msgs {
		if t, ok := topics[m.TopicID]; ok && m.Role == "user" {
			m.Content = topicLabel(t) + m.Content
		}
		out[i] = m
	}
	return out
}

func topicLabel(t store.Topic) string { return fmt.Sprintf("【话题#%d】", t.FirstSeq) }

// The supervisor's topic tools. The turn handles them itself, like
// stay_silent: they act on the turn's own state.
var (
	attachSpec = provider.ToolSpec{
		Name:        "topic_attach",
		Description: "把本轮插进来的【用户刚发来一条新消息】并进当前话题，一起处理。只用于对当前话题的补充或更正。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"message_id":{"type":"string","description":"插进来的那条消息的 id"}},"required":["message_id"]}`),
	}
	updateSpec = provider.ToolSpec{
		Name:        "topic_update",
		Description: "更新当前话题：summary 是“当前结论”，一两句现状（如“已修复，分支待合并；等你确认是否推送”）；title 不贴切时才改。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"title":{"type":"string"}}}`),
	}
	closeSpec = provider.ToolSpec{
		Name:        "topic_close",
		Description: "关闭当前话题，summary 一句话总结结果。有新消息时会自动重新打开。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","description":"一句话的结果"}},"required":["summary"]}`),
	}
)

// topicSpecs are added after the registry's tools, in a fixed order.
var topicSpecs = []provider.ToolSpec{attachSpec, closeSpec, updateSpec}

func isTopicTool(name string) bool {
	return name == attachSpec.Name || name == updateSpec.Name || name == closeSpec.Name
}

// readOnlyTools only look; anything else (including MCP tools) may change
// something, so it waits when the user has just written (R4).
var readOnlyTools = map[string]bool{
	"task_list": true, "task_result": true, "thread_search": true, "thread_read": true,
	"project_read": true, "git_status": true, "memory_search": true, "web_search": true,
	"web_fetch": true, "current_time": true, "machine_look": true, "mcp_list": true,
	"schedule_list": true,
}

// hasSideEffects reports whether any call may change something.
func hasSideEffects(calls []provider.ToolCall) bool {
	for _, c := range calls {
		if !readOnlyTools[c.Name] && !isTopicTool(c.Name) && c.Name != silentSpec.Name {
			return true
		}
	}
	return false
}

// heldNote answers each call that was not run because the user wrote.
const heldNote = "未执行：你决定这些操作的时候，用户发来了新消息（附在最后一个结果后面）。先看完它，再决定还要不要这样做；要的话重新调用。"

// absorbed is what a turn takes in from messages that arrived meanwhile.
type absorbed struct {
	note string
	seen []string
}

// absorb reads the messages after cursor and returns what to tell the
// model: the user's follow-ups in the turn's topic, and messages that just
// started a topic of their own, which may be follow-ups too (R3). Other
// input waits for its own turn.
func (s *Supervisor) absorb(ctx context.Context, tr *turnState) absorbed {
	msgs, err := s.opt.Log.Store.MessagesAfter(ctx, tr.thread, tr.cursor)
	if err != nil || len(msgs) == 0 {
		return absorbed{}
	}
	var b strings.Builder
	var out absorbed
	for _, m := range msgs {
		tr.cursor = max(tr.cursor, m.Seq)
		if m.Role != "user" || m.Kind != "" {
			continue
		}
		text := strings.TrimSpace(plain(m).Content)
		switch {
		case m.TopicID == tr.topic && tr.topic != "":
			fmt.Fprintf(&b, "\n\n【用户在这个话题里补充了一条消息（#%d）】\n%s\n【补充结束：按它调整你正在做的事】", m.Seq, text)
			tr.upTo = max(tr.upTo, m.Seq)
		case m.TopicID == m.ID && s.alone(ctx, m):
			b.WriteString(freshNote(m, text))
			tr.attachable[m.ID] = m.Seq
		default:
			continue
		}
		out.seen = append(out.seen, m.ID)
	}
	out.note = b.String()
	return out
}

// alone reports whether m is the only message of the topic it started.
func (s *Supervisor) alone(ctx context.Context, m store.Message) bool {
	msgs, err := s.opt.Log.Store.TopicMessages(ctx, m.ThreadID, m.ID, 0, 2)
	return err == nil && len(msgs) == 1
}

// seen tells the chat the supervisor has started on these messages.
func (s *Supervisor) seen(threadID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	s.opt.Log.Emit(event.New(event.MessageSeen, event.ActorSupervisor, threadID, event.MessageSeenPayload{MessageIDs: ids}))
}

// addNote appends text to the end of the request: to the last tool result
// when there is one (every provider takes that), else to the last message.
func addNote(req *provider.Request, text string) {
	if text == "" || len(req.Messages) == 0 {
		return
	}
	last := &req.Messages[len(req.Messages)-1]
	parts := append([]provider.Part{}, last.Parts...)
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].ToolResult != nil {
			r := *parts[i].ToolResult
			r.Content += text
			parts[i].ToolResult = &r
			last.Parts = parts
			return
		}
	}
	last.Parts = append(parts, provider.Part{Text: text})
}

// topicCall runs one of the topic tools.
func (s *Supervisor) topicCall(ctx context.Context, tr *turnState, c provider.ToolCall) provider.ToolResult {
	fail := func(msg string) provider.ToolResult {
		return provider.ToolResult{CallID: c.ID, Content: msg, IsError: true}
	}
	var a struct {
		MessageID string `json:"message_id"`
		Title     string `json:"title"`
		Summary   string `json:"summary"`
	}
	if err := json.Unmarshal(c.Args, &a); err != nil {
		return fail("参数不是合法的 JSON：" + err.Error())
	}
	if tr.topic == "" {
		return fail("这段对话没有话题（更早的消息），不需要整理话题。")
	}
	switch c.Name {
	case attachSpec.Name:
		seq, ok := tr.attachable[a.MessageID]
		if !ok {
			return fail("只能并入本轮刚插进来的新消息（看【用户刚发来一条新消息】里的 message_id）。")
		}
		ev := event.New(event.MessageMoved, event.ActorSupervisor, tr.thread,
			event.MessageMovedPayload{MessageID: a.MessageID, TopicID: tr.topic, From: a.MessageID})
		if _, err := s.opt.Log.Append(ctx, ev); err != nil {
			return fail(err.Error())
		}
		delete(tr.attachable, a.MessageID)
		tr.upTo = max(tr.upTo, seq)
		return provider.ToolResult{CallID: c.ID, Content: "已并入当前话题，这一轮一起处理它。"}
	case updateSpec.Name:
		p := event.TopicUpdatedPayload{TopicID: tr.topic}
		if t := strings.TrimSpace(a.Title); t != "" {
			p.Title = &t
		}
		if sum := strings.TrimSpace(a.Summary); sum != "" {
			p.Summary = &sum
		}
		if p.Title == nil && p.Summary == nil {
			return fail("summary 和 title 至少填一个。")
		}
		if _, err := s.opt.Log.Append(ctx, event.New(event.TopicUpdated, event.ActorSupervisor, tr.thread, p)); err != nil {
			return fail(err.Error())
		}
		return provider.ToolResult{CallID: c.ID, Content: "已更新"}
	case closeSpec.Name:
		sum := strings.TrimSpace(a.Summary)
		if sum == "" {
			return fail("关闭话题要用一句话总结结果（summary）。")
		}
		closed := event.TopicClosed
		ev := event.New(event.TopicUpdated, event.ActorSupervisor, tr.thread, event.TopicUpdatedPayload{TopicID: tr.topic, Summary: &sum, Status: &closed})
		if _, err := s.opt.Log.Append(ctx, ev); err != nil {
			return fail(err.Error())
		}
		return provider.ToolResult{CallID: c.ID, Content: "话题已关闭"}
	}
	return fail("unknown tool")
}

// topicEnv describes the turn's topic for the tail.
func (s *Supervisor) topicEnv(ctx context.Context, topicID string) *prompts.TopicEnv {
	if topicID == "" {
		return nil
	}
	st := s.opt.Log.Store
	t, err := st.Topic(ctx, topicID)
	if err != nil {
		return nil
	}
	env := &prompts.TopicEnv{Label: topicLabel(t), Title: t.Title, Summary: t.Summary, Closed: t.Status == event.TopicClosed}
	if tasks, err := st.TopicTasks(ctx, []string{topicID}); err == nil {
		for _, task := range tasks[topicID] {
			line := fmt.Sprintf("%s「%s」%s", task.ID, task.Title, task.Status)
			if task.StatusReason != "" {
				line += "（" + task.StatusReason + "）"
			}
			env.Tasks = append(env.Tasks, line)
		}
	}
	return env
}

// freshNote offers a message that started a topic of its own as a possible
// follow-up of the turn's topic.
func freshNote(m store.Message, text string) string {
	return fmt.Sprintf("\n\n【用户刚发来一条新消息（#%d，message_id %s），界面上暂时是一个新话题】\n%s\n"+
		"【如果它是对当前话题的补充或更正，调用 topic_attach 把它并进来，并在这一轮一起处理；如果是另一件事，不要处理它，它会在这一轮之后单独处理】",
		m.Seq, m.ID, text)
}

// offerFresh is absorb for what came before the turn started: messages the
// user sent right after the turn's topic, each starting a topic of its own,
// may be follow-ups of it (several quick messages from the main input).
func offerFresh(msgs []store.Message, tr *turnState, pick pendingTopic) (string, []string) {
	count := map[string]int{}
	for _, m := range msgs {
		count[m.TopicID]++
	}
	var b strings.Builder
	var ids []string
	for _, m := range msgs {
		if m.Role != "user" || m.Kind != "" || m.TopicID == tr.topic || m.TopicID != m.ID || count[m.ID] != 1 || m.Seq < pick.Oldest {
			continue
		}
		b.WriteString(freshNote(m, strings.TrimSpace(plain(m).Content)))
		tr.attachable[m.ID] = m.Seq
		ids = append(ids, m.ID)
	}
	return b.String(), ids
}

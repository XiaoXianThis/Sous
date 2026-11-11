package supervisor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/prompts"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

var meetingTools = []string{"project_read", "consult", "web_search", "web_fetch", "memory_search", "thread_search", "thread_read", "current_time"}

func isMeeting(t store.Thread) bool { return t.Kind == event.KindMeeting }

var minuteRef = regexp.MustCompile(`\[#(\d+)\]`)
var minuteEnd = regexp.MustCompile(`(?:\[#\d+\])+$`)

func checkMinutes(content string, seqs map[int64]bool) []string {
	sections := strings.Split(prompts.MinutesSections, "\n")
	next := 0
	current := ""
	var problems []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			current = line
			if next >= len(sections) || line != sections[next] {
				problems = append(problems, "栏目缺失或顺序错误："+line)
			} else {
				next++
			}
			continue
		}
		if current != "## 结论和决定" && current != "## 行动项" {
			continue
		}
		if !strings.HasPrefix(line, "- ") || strings.TrimSpace(strings.TrimPrefix(line, "- ")) == "无" {
			continue
		}
		refs := minuteRef.FindAllStringSubmatch(line, -1)
		valid := len(refs) > 0 && minuteEnd.MatchString(line)
		for _, ref := range refs {
			n, _ := strconv.ParseInt(ref[1], 10, 64)
			if !seqs[n] {
				valid = false
			}
		}
		if !valid {
			problems = append(problems, "缺少有效出处："+line)
		}
	}
	if next < len(sections) {
		problems = append(problems, "缺少栏目："+strings.Join(sections[next:], "、"))
	}
	return problems
}
func meetingTranscript(msgs []store.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Kind == event.KindSilent || m.Kind == event.KindStep {
			continue
		}
		who := "用户"
		if m.Role == "assistant" {
			who = "主管"
		}
		if m.Kind == "consult" {
			who = "项目顾问"
		}
		if m.Kind == "dm" && m.Role == "assistant" && m.Source != nil {
			who = m.Source.Agent
		}
		if m.Kind == event.KindTrigger {
			who = "系统"
		}
		fmt.Fprintf(&b, "[#%d] %s %s：%s\n\n", m.Seq, time.UnixMilli(m.CreatedAt).Format("2006-01-02 15:04"), who, m.Content)
	}
	return strings.TrimSpace(b.String())
}
func (s *Supervisor) writeMinutes(id string) {
	ctx := s.ctx
	err := s.makeMinutes(ctx, id)
	if err == nil {
		return
	}
	line := strings.SplitN(err.Error(), "\n", 2)[0]
	_, _ = s.opt.Log.Append(ctx, event.New(event.MessageAssistant, event.ActorSystem, id, event.MessagePayload{MessageID: ids.New(), Kind: event.KindNotice, Content: "写纪要失败：" + line + "。可以再点一次“散会”重试。"}), event.New(event.MeetingReopened, event.ActorSystem, id, event.MeetingPayload{MeetingID: id}))
}
func (s *Supervisor) makeMinutes(ctx context.Context, id string) error {
	thread, err := s.opt.Log.Store.Thread(ctx, id)
	if err != nil {
		return err
	}
	var seg store.Segment
	var msgs []store.Message
	var input string
	for attempt := 0; attempt < 2; attempt++ {
		seg, err = s.segment(ctx, id)
		if err != nil {
			return err
		}
		msgs, err = s.opt.Log.Store.MessagesAfter(ctx, id, seg.CoversUpTo)
		if err != nil {
			return err
		}
		input = meetingTranscript(msgs)
		if seg.Summary != "" {
			input = "【之前的讨论摘要】\n" + seg.Summary + "\n\n" + input
		}
		if attempt == 0 && estimateText(input) > int(.6*float64(s.window(ctx))) {
			if err = s.compact(ctx, id, event.CompactManual); err != nil {
				return err
			}
			continue
		}
		break
	}
	seqs := map[int64]bool{}
	for _, m := range msgs {
		if m.Kind != event.KindSilent && m.Kind != event.KindStep {
			seqs[m.Seq] = true
		}
	}
	for _, ref := range minuteRef.FindAllStringSubmatch(seg.Summary, -1) {
		n, _ := strconv.ParseInt(ref[1], 10, 64)
		seqs[n] = true
	}
	p := s.opt.Compactor
	model := s.opt.CompactorModel
	if p == nil {
		p = s.opt.Provider
		model = s.opt.Model
	}
	if p == nil {
		return errors.New("没有整理模型")
	}
	req := provider.Request{Model: model, SessionID: id + ":minutes", System: prompts.Minutes(thread.Title), Messages: []provider.Message{provider.Text(provider.RoleUser, input)}}
	var text string
	var problems []string
	for attempt := 0; attempt < 2; attempt++ {
		resp, e := p.Stream(ctx, req, nil)
		if e != nil {
			return e
		}
		s.record(id, RoleCompactor, provider.ModelOf(resp, model), p, resp)
		text = strings.TrimSpace(resp.Message.PlainText())
		if text == "" {
			return errors.New("整理模型没有返回纪要")
		}
		problems = checkMinutes(text, seqs)
		if len(problems) == 0 {
			break
		}
		if attempt == 0 {
			req.Messages = []provider.Message{provider.Text(provider.RoleUser, input+"\n\n上一次的纪要有这些问题，改正后重新输出完整的纪要：\n- "+strings.Join(problems, "\n- "))}
		}
	}
	if len(problems) > 0 {
		missing := 0
		for _, p := range problems {
			if strings.HasPrefix(p, "缺少有效出处") {
				missing++
			}
		}
		text += fmt.Sprintf("\n\n（有 %d 条没有标出处）", missing)
	}
	rows, err := s.opt.Log.Store.Minutes(ctx, id)
	if err != nil {
		return err
	}
	version := 1
	if len(rows) > 0 {
		version = rows[len(rows)-1].Version + 1
	}
	if _, err = s.opt.Log.Append(ctx, event.New(event.MinutesDrafted, event.ActorSystem, id, event.MinutesPayload{MeetingID: id, Version: version, Content: text})); err != nil {
		return err
	}
	if setting, _ := s.opt.Log.Store.Setting(ctx, "meeting.minutes_auto_post"); setting == "true" {
		_, err = s.postMinutes(ctx, id, "")
	}
	return err
}

var errNoDraft = errors.New("没有纪要草稿")

func (s *Supervisor) postMinutes(ctx context.Context, id, content string) (string, error) {
	st := s.opt.Log.Store
	thread, err := st.Thread(ctx, id)
	if err != nil {
		return "", err
	}
	rows, err := st.Minutes(ctx, id)
	if err != nil {
		return "", err
	}
	var draft *store.MinutesRow
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Status == "draft" {
			draft = &rows[i]
			break
		}
	}
	if draft == nil {
		return "", errNoDraft
	}
	if content == "" {
		content = draft.Content
	}
	heading := "## 会议纪要：" + thread.Title
	if draft.Version > 1 {
		heading += fmt.Sprintf("（第 %d 版，取代第 %d 版）", draft.Version, draft.Version-1)
	}
	body := heading + "\n\n" + content
	msgID := ids.New()
	topic := thread.ParentTopicID
	if topic == "" {
		topic = msgID
	}
	triggerID := ids.New()
	notice := fmt.Sprintf("【会议结束】会议「%s」的纪要（第 %d 版）已经发到这个话题。\n看一下行动项：要建任务、动代码或做 git 操作的，先和用户确认再做；不清楚的地方用 thread_read（thread=\"%s\"，seq=纪要里的编号）查会议原文。\n（这是 sous 的自动通知，不是用户发的消息。）", thread.Title, draft.Version, id)
	_, err = s.opt.Log.Write(ctx, func(w *store.Writer) error {
		_, e := w.Append(event.New(event.MessageAssistant, event.ActorSupervisor, thread.ParentThreadID, event.MessagePayload{MessageID: msgID, Kind: event.KindMinutes, Content: body, Source: &event.MessageSource{MeetingID: id, Version: draft.Version}, TopicID: thread.ParentTopicID, TopicTitle: "会议纪要：" + thread.Title}))
		if e != nil {
			return e
		}
		_, e = w.Append(event.New(event.MinutesPosted, event.ActorUser, id, event.MinutesPayload{MeetingID: id, Version: draft.Version, Content: content, MessageID: msgID}))
		if e != nil {
			return e
		}
		_, e = w.Append(event.New(event.TriggerFired, event.ActorSystem, thread.ParentThreadID, event.TriggerPayload{Kind: event.TriggerMeeting, Key: fmt.Sprintf("meeting:%s:%d", id, draft.Version), MessageID: triggerID, Title: "会议结束", Content: notice, TopicID: topic}))
		return e
	})
	return msgID, err
}

var meetingProposeSpec = provider.ToolSpec{
	Name:        "meeting_propose",
	Description: "用户的需求还不确定、需要来回讨论很多轮时，提议开一个会：会议在独立的上下文里讨论，散会后纪要回到这个话题。只是提议，用户点了“开会”才开始。",
	Schema:      []byte(`{"type":"object","properties":{"question":{"type":"string","description":"会议要讨论清楚的问题，一两句话"}},"required":["question"]}`),
}

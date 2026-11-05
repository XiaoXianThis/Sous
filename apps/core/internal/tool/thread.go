package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// The supervisor sees a thread's older messages only as a summary
// (long-main-thread); these tools read the originals back.

const (
	searchLimit  = 10
	snippetRunes = 160
	readMax      = 20
	readRunes    = 6000 // a long message is cut here; read it alone for more
)

// ThreadSearch finds earlier messages of the conversation.
type ThreadSearch struct{ Log *eventlog.Log }

func (ThreadSearch) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "thread_search",
		Description: "在这条对话以前的消息里搜索（包括已经折叠进摘要的原文），返回编号、时间和片段。" +
			"需要更早的原话、数字、链接或细节时用；再用 thread_read 按编号读全文。scope=all 在所有对话里搜（只在主线可用）。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"query":{"type":"string","description":"关键词，几个词用空格隔开"},
			"limit":{"type":"integer","description":"最多返回几条，默认 10"},
			"scope":{"type":"string","enum":["thread","all"]}
		},"required":["query"]}`),
	}
}

func (t ThreadSearch) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Query string
		Limit int
		Scope string
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	turn := TurnFrom(ctx)
	thread := turn.ThreadID
	if a.Scope == "all" {
		if turn.ThreadID != turn.MainThreadID {
			return "", errors.New("scope=all 只能在主线里用")
		}
		thread = ""
	}
	if a.Limit <= 0 || a.Limit > searchLimit {
		a.Limit = searchLimit
	}
	hits, err := t.Log.Store.SearchMessages(ctx, thread, a.Query, a.Limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "没有找到相关的消息。", nil
	}
	lines := make([]string, len(hits))
	for i, h := range hits {
		line := formatMessage(h.Message, snippet(h.Content, a.Query))
		if thread == "" && h.ThreadID != turn.ThreadID {
			line += "（另一条对话）"
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n"), nil
}

// ThreadRead reads earlier messages of the conversation in full.
type ThreadRead struct{ Log *eventlog.Log }

func (ThreadRead) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "thread_read",
		Description: "按编号读这条对话里某条消息的全文，以及它前后的几条消息。编号来自摘要里的 [#数字] 或 thread_search 的结果。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"seq":{"type":"integer","description":"消息编号"},
			"before":{"type":"integer","description":"再读它前面几条，默认 3"},
			"after":{"type":"integer","description":"再读它后面几条，默认 3"}
		},"required":["seq"]}`),
	}
}

func (t ThreadRead) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Seq    int64
		Before *int
		After  *int
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	before, after := 3, 3
	if a.Before != nil {
		before = max(0, *a.Before)
	}
	if a.After != nil {
		after = max(0, *a.After)
	}
	for before+after+1 > readMax {
		if before > after {
			before--
		} else {
			after--
		}
	}
	turn := TurnFrom(ctx)
	msgs, err := t.Log.Store.MessagesAround(ctx, turn.ThreadID, a.Seq, before, after)
	if errors.Is(err, store.ErrNotFound) && turn.ThreadID == turn.MainThreadID {
		// From thread_search with scope=all: read it in its own thread.
		if m, err2 := t.Log.Store.MessageBySeq(ctx, a.Seq); err2 == nil {
			msgs, err = t.Log.Store.MessagesAround(ctx, m.ThreadID, a.Seq, before, after)
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("这条对话里没有编号 #%d 的消息", a.Seq)
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range msgs {
		if m.Kind == event.KindSilent {
			continue
		}
		text := m.Content
		limit := readRunes
		if m.Seq != a.Seq {
			limit = readRunes / 4
		}
		if r := []rune(text); len(r) > limit {
			text = string(r[:limit]) + fmt.Sprintf("……（后面还有 %d 字", len(r)-limit)
			if m.Seq != a.Seq {
				text += "，单独读这一条看全文）"
			} else {
				text += "）"
			}
		}
		b.WriteString(formatMessage(m, text))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func formatMessage(m store.Message, text string) string {
	who := "用户"
	switch {
	case m.Role == "assistant":
		who = "主管"
	case m.Kind == event.KindTrigger:
		who = "系统"
	}
	return fmt.Sprintf("[#%d] %s %s：%s", m.Seq, time.UnixMilli(m.CreatedAt).Format("2006-01-02 15:04"), who, text)
}

// snippet is the part of s around the first query word it contains.
func snippet(s, query string) string {
	r := []rune(s)
	if len(r) <= snippetRunes {
		return s
	}
	at := 0
	lower := strings.ToLower(s)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if i := strings.Index(lower, w); i >= 0 {
			at = len([]rune(lower[:i]))
			break
		}
	}
	start := max(0, at-snippetRunes/3)
	end := min(len(r), start+snippetRunes)
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

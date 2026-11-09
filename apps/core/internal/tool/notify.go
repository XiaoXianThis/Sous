package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
)

// Notify posts a notice to the main thread, where clients alert the user.
// Web Push delivery joins it in milestone M3.
type Notify struct{ Log *eventlog.Log }

func (Notify) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "notify",
		Description: "主动提醒用户：发到主线，并在用户的设备上弹出通知。只用于用户必须知道或必须行动的事，" +
			"不要用来重复正在对话里说的内容。",
		Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"一句话说清楚发生了什么、需要用户做什么"}},"required":["text"]}`),
	}
}

func (t Notify) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Text string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	a.Text = strings.TrimSpace(a.Text)
	if a.Text == "" {
		return "", errors.New("text is empty")
	}
	turn := TurnFrom(ctx)
	main := turn.MainThreadID
	topic := ""
	if turn.ThreadID == main {
		topic = turn.TopicID // the topic the turn is about, if it is the main thread's
	}
	if main == "" {
		var err error
		if main, err = t.Log.Store.MainThreadID(ctx); err != nil {
			return "", err
		}
	}
	ev := event.New(event.MessageAssistant, event.ActorSupervisor, main,
		event.MessagePayload{MessageID: ids.New(), Content: a.Text, Kind: event.KindNotice, TopicID: topic})
	if _, err := t.Log.Append(ctx, ev); err != nil {
		return "", err
	}
	return "已通知用户", nil
}

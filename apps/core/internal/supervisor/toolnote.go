package supervisor

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	// noteValueRunes bounds one argument value in a tool note (machine_look's
	// command keeps its first 80 characters).
	noteValueRunes = 80
	// noteArgsRunes bounds the whole argument summary.
	noteArgsRunes = 160
	// noteErrorRunes bounds the one-line error of a failed call.
	noteErrorRunes  = 120
	noteResultRunes = 400
)

// toolNote describes one call of the supervisor for the chat.
func toolNote(c provider.ToolCall, r provider.ToolResult) event.ToolNote {
	n := event.ToolNote{Name: c.Name, Args: argsSummary(c.Args)}
	if r.IsError {
		n.Error = clipRunes(firstLine(redact.Mask(r.Content)), noteErrorRunes)
		if n.Error == "" {
			n.Error = "失败"
		}
	} else {
		n.Result = clipRunes(firstLine(redact.Mask(r.Content)), noteResultRunes)
	}
	return n
}

// argsSummary is "key=value" for each argument, in key order: values cut to
// noteValueRunes, values of fields named like credentials replaced by ***,
// and anything else that looks like a credential masked.
func argsSummary(raw json.RawMessage) string {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return clipRunes(oneLine(redact.Mask(string(raw))), noteArgsRunes)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var v string
		switch x := obj[k].(type) {
		case string:
			v = x
		case nil:
			continue
		default:
			b, _ := json.Marshal(x)
			v = string(b)
		}
		if redact.SecretName(k) {
			v = "***"
		}
		parts = append(parts, k+"="+clipRunes(oneLine(redact.Mask(v)), noteValueRunes))
	}
	return clipRunes(strings.Join(parts, " "), noteArgsRunes)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clipRunes cuts s to n runes, marking the cut with "…".
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// historyText keeps tool evidence beside the words it qualifies. Old rows
// without a result are explicitly unknown, rather than implied successes.
func historyText(m store.Message) string {
	text := m.Content
	if m.Error || m.Kind == event.KindSilent {
		text = ""
	}
	if m.Role != "assistant" {
		return text
	}
	for _, n := range m.Tools {
		result := n.Result
		if n.Error != "" {
			result = "失败：" + n.Error
		}
		if result == "" {
			result = "未保存结果，不能据此确认执行成功"
		}
		text += "\n【本轮工具】" + n.Name + " → " + result
	}
	return text
}

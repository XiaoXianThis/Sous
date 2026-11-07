package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
)

// resultPrompt ends every work run's prompt: a fixed closing section the
// Hub reads back (decision 0025).
const resultPrompt = "\n完成后，在最后用下面的格式收尾（每项一两句；没有就写“无”）：\n" +
	"## 结果\n" +
	"- 做了：…\n" +
	"- 改动：…（文件或命令）\n" +
	"- 验证：…（跑了什么、结果怎样；没验证就写“没有验证”）\n" +
	"- 没做完 / 风险：…\n" +
	"- 需要决定：…（需要用户或主管拿主意的事）\n"

// Result is a work run's closing section.
type Result struct {
	Done, Changed, Verified, Left, Decide string
}

var resultFields = []struct {
	label string
	field func(*Result) *string
}{
	{"做了", func(r *Result) *string { return &r.Done }},
	{"改动", func(r *Result) *string { return &r.Changed }},
	{"验证", func(r *Result) *string { return &r.Verified }},
	{"没做完", func(r *Result) *string { return &r.Left }},
	{"风险", func(r *Result) *string { return &r.Left }},
	{"需要决定", func(r *Result) *string { return &r.Decide }},
}

// ParseResult reads the last "## 结果" section of a run's summary.
func ParseResult(summary string) (Result, bool) {
	i := strings.LastIndex(summary, "结果")
	for i >= 0 {
		line := summary[strings.LastIndex(summary[:i], "\n")+1:]
		if j := strings.IndexByte(line, '\n'); j >= 0 {
			line = line[:j]
		}
		if h := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "#*：: ")); h == "结果" {
			break
		}
		i = strings.LastIndex(summary[:i], "结果")
	}
	if i < 0 {
		return Result{}, false
	}
	body := summary[i+len("结果"):]
	if j := strings.IndexByte(body, '\n'); j >= 0 {
		body = body[j+1:]
	} else {
		body = ""
	}
	var r Result
	var cur *string
	found := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			break // the next section
		}
		item := strings.TrimSpace(strings.TrimLeft(t, "-*• "))
		matched := false
		if t != item || cur == nil {
			for _, f := range resultFields {
				if !strings.HasPrefix(strings.Trim(item, "*"), f.label) {
					continue
				}
				rest := strings.Trim(item, "*")[len(f.label):]
				if k := strings.IndexAny(rest, "：:"); k >= 0 && k <= len(" / 风险") {
					rest = rest[k:]
					rest = strings.TrimLeft(rest, "：:")
				} else {
					continue
				}
				cur = f.field(&r)
				*cur = joinText(*cur, strings.TrimSpace(strings.Trim(rest, "* ")))
				matched, found = true, true
				break
			}
		}
		if !matched && cur != nil && t != "" {
			*cur = joinText(*cur, t)
		}
	}
	return r, found
}

func joinText(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " " + b
}

// none reports whether a field says there is nothing.
func none(s string) bool {
	s = strings.Trim(strings.TrimSpace(s), "。.")
	return s == "" || s == "无" || s == "没有" || s == "暂无" || strings.EqualFold(s, "none") || s == "-"
}

// Text lays the result out for a report, as a Markdown list.
func (r Result) Text() string {
	var b strings.Builder
	line := func(label, v string) {
		if !none(v) {
			fmt.Fprintf(&b, "- %s：%s\n", label, v) // a list: reports are Markdown
		}
	}
	line("做了", r.Done)
	line("改动", r.Changed)
	line("验证", r.Verified)
	line("没做完 / 风险", r.Left)
	line("需要决定", r.Decide)
	return strings.TrimSpace(b.String())
}

// summaryText is a run's summary for a report: its result section when
// it has one, else the summary cut short.
func summaryText(summary string, n int) string {
	if r, ok := ParseResult(summary); ok {
		if t := r.Text(); t != "" {
			return t
		}
	}
	return truncate(summary, n)
}

// resultTool lets the supervisor read what a task's run did in full.
type resultTool struct{ e *Engine }

func (resultTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "task_result",
		Description: "看一个任务做了什么：agent 的完整总结、改动的 diff（可以只看一个文件），或者执行过程（调用了哪些工具、结果如何）。" +
			"汇报里只有摘要；要判断下一步、回答用户的细节问题、决定要不要返工时用它。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"task":{"type":"string","description":"任务 id"},
			"part":{"type":"string","enum":["summary","diff","log"],"description":"summary 完整总结和结果；diff 改动；log 执行过程"},
			"file":{"type":"string","description":"part=diff 时只看这个文件"}
		},"required":["task","part"]}`),
	}
}

const maxResultBytes = 24 * 1024

func (t resultTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Task, Part, File string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if err := t.e.checkScope(ctx, a.Task); err != nil {
		return "", err
	}
	task, err := t.e.store().Task(ctx, a.Task)
	if err != nil {
		return "", fmt.Errorf("%w: 没有任务 %s", ErrInvalid, a.Task)
	}
	r := t.e.lastRun(ctx, task.ID, event.PurposeWork, event.PurposeRebase)
	if r.ID == "" {
		return "任务还没有执行过", nil
	}
	head := fmt.Sprintf("任务 %s「%s」（%s），最近一次执行 %s（%s，%s 上的 %s）\n", task.ID, task.Title, statusName[task.Status], r.ID, r.Status, r.NodeID, r.Agent)
	switch a.Part {
	case "diff":
		diff, err := os.ReadFile(t.e.Nodes.DiffPath(r.ID))
		if err != nil || len(diff) == 0 {
			return head + "这次执行没有改动（或者改动不在 git 里）", nil
		}
		text := string(diff)
		if a.File != "" {
			text = fileDiff(text, a.File)
			if text == "" {
				return head + "diff 里没有文件 " + a.File + "；改动的文件：" + r.DiffStat, nil
			}
		}
		return head + clip(text, maxResultBytes), nil
	case "log":
		return head + t.e.runLog(ctx, r.ID), nil
	default:
		var b strings.Builder
		b.WriteString(head)
		if r.DiffStat != "" {
			b.WriteString("改动：" + r.DiffStat + "\n")
		}
		if r.Error != "" {
			b.WriteString("错误：" + r.Error + "\n")
		}
		if len(r.Refused) > 0 {
			b.WriteString("被拒绝的操作：" + strings.Join(r.Refused, "；") + "\n")
		}
		if task.ReviewNote != "" {
			b.WriteString("验收意见：" + task.ReviewNote + "\n")
		}
		b.WriteString("\nagent 的总结：\n" + firstNonEmpty(r.Summary, "（没有）"))
		return clip(b.String(), maxResultBytes), nil
	}
}

// fileDiff is one file's part of a unified diff.
func fileDiff(diff, file string) string {
	var out []string
	in := false
	for _, line := range strings.SplitAfter(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			in = strings.HasSuffix(strings.TrimSpace(line), " b/"+file) || strings.Contains(line, " a/"+file+" ")
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}

// runLog renders a run's tool calls and status lines, newest last.
func (e *Engine) runLog(ctx context.Context, runID string) string {
	evs, err := e.store().RunEvents(ctx, runID)
	if err != nil {
		return "读不到执行过程：" + err.Error()
	}
	var lines []string
	last := map[string]int{} // tool call id → its line, so updates replace it
	for _, ev := range evs {
		if ev.Type != event.RunUpdate {
			continue
		}
		var p event.RunUpdatePayload
		if ev.Decode(&p) != nil {
			continue
		}
		switch p.Kind {
		case "tool":
			line := "工具：" + firstNonEmpty(p.Title, p.ToolKind, "?")
			if p.Status != "" {
				line += "（" + p.Status + "）"
			}
			if i, ok := last[p.ToolCallID]; ok && p.ToolCallID != "" {
				if p.Title == "" {
					line = strings.SplitN(lines[i], "（", 2)[0]
					if p.Status != "" {
						line += "（" + p.Status + "）"
					}
				}
				lines[i] = line
				continue
			}
			last[p.ToolCallID] = len(lines)
			lines = append(lines, line)
		case "status":
			lines = append(lines, "状态："+p.Text)
		case "permission":
			lines = append(lines, "审批："+p.Title+" → "+p.Decision)
		}
	}
	if len(lines) == 0 {
		return "没有记录到工具调用"
	}
	if len(lines) > 120 {
		lines = append([]string{fmt.Sprintf("（前面还有 %d 条）", len(lines)-120)}, lines[len(lines)-120:]...)
	}
	return clip(strings.Join(lines, "\n"), maxResultBytes)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…（还有 %d 字节没有显示）", len(s)-cut)
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

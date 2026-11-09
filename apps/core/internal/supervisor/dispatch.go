package supervisor

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
)

// Catch explicit completion claims, not plans such as “准备派发” or
// “还没派发”. This is a backstop; the tool result remains the authority.
var dispatchClaim = regexp.MustCompile(`(?:已(?:经)?|刚刚|刚才)(?:[^。！？\n，,；;]{0,24})(?:派发|派给|派出)|(?:任务|task)[^。！？\n，,；;]{0,24}(?:派好了|派完了|已交给|已经交给)`)
var dispatchQuote = regexp.MustCompile("“[^”]*”|「[^」]*」|\"[^\"]*\"|`[^`]*`")

func claimsDispatch(text string) bool {
	// Quoting a past claim or reporting that dispatch has not happened
	// must not cause the supervisor to create work again.
	text = dispatchQuote.ReplaceAllString(text, "")
	for _, claim := range dispatchClaim.FindAllString(text, -1) {
		negated := strings.ContainsAny(claim, "未没")
		for _, word := range []string{"准备", "打算", "计划", "不能", "无法", "失败"} {
			negated = negated || strings.Contains(claim, word)
		}
		if !negated {
			return true
		}
	}
	return false
}

const dispatchCheckNote = "【sous 派发核验】本轮没有成功的派发工具回执，不能声称已派发。若用户已授权新任务，请实际调用 task_create；失败或已创建时先核对 task_list，复用原任务 id，修正后 task_dispatch，不要重复创建。若只是查询旧任务，核对后如实说明其当前状态。排队不等于 agent 已启动。"
const dispatchUnconfirmed = "本轮未取得成功的派发回执，无法确认任务已派出。请查看任务列表中的实际状态。"

func hasDispatchTools(specs []provider.ToolSpec) bool {
	for _, s := range specs {
		if s.Name == "task_create" || s.Name == "task_dispatch" {
			return true
		}
	}
	return false
}

func dispatchReceipt(c provider.ToolCall, r provider.ToolResult) bool {
	if r.IsError {
		return false
	}
	switch c.Name {
	case "task_dispatch":
		return true
	case "task_create":
		var a struct{ Dispatch *bool }
		return json.Unmarshal(c.Args, &a) == nil && (a.Dispatch == nil || *a.Dispatch)
	}
	return false
}

package nodehub

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Environment describes the machines for the supervisor's prompt, one line each.
func (h *Hub) Environment(ctx context.Context) []string {
	nodes, err := h.log.Store.Nodes(ctx)
	if err != nil || len(nodes) == 0 {
		return []string{"还没有配对任何电脑；要派 agent 干活，先让用户在“机器”页面配对一台。"}
	}
	var out, models []string
	listed := map[string]bool{} // an agent's choices, listed once for all machines
	for _, n := range nodes {
		state := "离线"
		if h.Online(n.ID) {
			state = "在线"
		}
		var agents, off []string
		for _, a := range parseAgents(n.Agents) {
			if !a.Installed {
				continue
			}
			if a.Disabled {
				off = append(off, a.Name)
				continue
			}
			s := a.Name
			var notes []string
			if a.Default {
				notes = append(notes, "这台的默认")
			}
			if a.LoggedIn != nil && !*a.LoggedIn {
				notes = append(notes, "未登录")
			}
			if m := modelsOf(a); m != "" && !listed[a.Name+m] {
				listed[a.Name+m] = true
				models = append(models, fmt.Sprintf("%s 可选的%s", a.Name, m))
			}
			if len(notes) > 0 {
				s += "（" + strings.Join(notes, "；") + "）"
			}
			agents = append(agents, s)
		}
		sort.Strings(agents)
		sort.Strings(off)
		list := "无"
		if len(agents) > 0 {
			list = strings.Join(agents, "、")
		}
		role := "开发机"
		if n.Role == "server" {
			role = "服务器（只接运维任务）"
		}
		line := fmt.Sprintf("机器 %s（id %s，%s，%s/%s，%s）：可用 agent %s", n.Name, n.ID, role, n.OS, n.Arch, state, list)
		if len(off) > 0 {
			line += "，停用的 agent " + strings.Join(off, "、") + "（不能用）"
		}
		capacity := n.Concurrency
		if capacity <= 0 {
			capacity = 2 // the router's default
		}
		line += fmt.Sprintf("，同时最多执行 %d 个 Run，现在有 %d 个在执行或排队（包括别的对话派的）", capacity, h.Busy(ctx, n.ID))
		if len(n.Workspaces) > 0 {
			line += "，工作空间 " + strings.Join(n.Workspaces, "、")
		} else {
			line += "，没有设置工作空间（不能 clone 或新建项目）"
		}
		if n.Wok == "" && n.Role != "server" {
			line += "，没装 wok"
		}
		out = append(out, line)
	}
	// What task_create's model and effort can name, per agent (decision
	// 0024); "（默认）" marks what the agent uses when nothing is set.
	return append(out, models...)
}

// maxModelsShown bounds the models listed per agent.
const maxModelsShown = 80

// modelsOf lists the models an agent offers (from its last session), and
// its reasoning levels: what task_create's model and effort can pick.
func modelsOf(a proto.AgentInfo) string {
	var parts []string
	for _, o := range a.Options {
		var label string
		switch o.Category {
		case "model":
			label = "模型："
		case "thought_level":
			label = "思考强度："
		default:
			continue
		}
		var vs []string
		for i, v := range o.Values {
			if i == maxModelsShown {
				vs = append(vs, fmt.Sprintf("…共 %d 个", len(o.Values)))
				break
			}
			name := v.Value
			if v.Value == o.Current {
				name += "（默认）"
			}
			vs = append(vs, name)
		}
		if len(vs) > 0 {
			parts = append(parts, label+strings.Join(vs, " / "))
		}
	}
	return strings.Join(parts, "；")
}

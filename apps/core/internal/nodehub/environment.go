package nodehub

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Environment describes the machines for the supervisor's prompt, one line each.
func (h *Hub) Environment(ctx context.Context) []string {
	nodes, err := h.log.Store.Nodes(ctx)
	if err != nil || len(nodes) == 0 {
		return []string{"还没有配对任何电脑；要派 agent 干活，先让用户在“机器”页面配对一台。"}
	}
	var out []string
	for _, n := range nodes {
		state := "离线"
		if h.Online(n.ID) {
			state = "在线"
		}
		var agents []string
		for _, a := range parseAgents(n.Agents) {
			if !a.Installed {
				continue
			}
			s := a.Name
			if a.LoggedIn != nil && !*a.LoggedIn {
				s += "（未登录）"
			}
			agents = append(agents, s)
		}
		sort.Strings(agents)
		list := "无"
		if len(agents) > 0 {
			list = strings.Join(agents, "、")
		}
		role := "开发机"
		if n.Role == "server" {
			role = "服务器（只接运维任务）"
		}
		line := fmt.Sprintf("机器 %s（id %s，%s，%s/%s，%s）：可用 agent %s", n.Name, n.ID, role, n.OS, n.Arch, state, list)
		capacity := n.Concurrency
		if capacity <= 0 {
			capacity = 2 // the router's default
		}
		line += fmt.Sprintf("，同时最多执行 %d 个 Run，现在有 %d 个在执行或排队（包括别的对话派的）", capacity, h.Busy(ctx, n.ID))
		if len(n.Workspaces) > 0 {
			line += "，工作空间 " + strings.Join(n.Workspaces, "、")
		} else if n.Role != "server" {
			line += "，没有设置工作空间（不能 clone 或新建项目）"
		}
		if n.Wok == "" && n.Role != "server" {
			line += "，没装 wok"
		}
		out = append(out, line)
	}
	return out
}

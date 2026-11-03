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
		out = append(out, fmt.Sprintf("机器 %s（id %s，%s/%s，%s）：可用 agent %s", n.Name, n.ID, n.OS, n.Arch, state, list))
	}
	return out
}

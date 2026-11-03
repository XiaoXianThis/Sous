package nodehub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// RunAgentTool lets the supervisor start a coding agent on a machine.
type RunAgentTool struct{ Hub *Hub }

func (RunAgentTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "run_agent",
		Description: "派一个编程 agent 到用户的某台电脑上干活。代码任务给出仓库在那台电脑上的绝对路径，" +
			"agent 会在这个仓库的独立 git worktree 和新分支里工作，不会动用户的工作区；不涉及仓库的一般任务不填 repo_path。" +
			"prompt 要写清楚目标、范围和完成标准。agent 要做改动时会请用户审批。派发后进度会直接显示在对话里，不用轮询。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"agent":{"type":"string","description":"claude / codex / cursor / opencode / kiro，见环境里的机器列表"},
			"prompt":{"type":"string","description":"交给 agent 的完整任务说明"},
			"repo_path":{"type":"string","description":"仓库在那台电脑上的绝对路径；一般任务不填"},
			"machine":{"type":"string","description":"机器名或 id；不填则选第一台在线且装了该 agent 的机器"}
		},"required":["agent","prompt"]}`),
	}
}

func (t RunAgentTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Agent    string `json:"agent"`
		Prompt   string `json:"prompt"`
		RepoPath string `json:"repo_path"`
		Machine  string `json:"machine"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	nodeID := ""
	if m := strings.TrimSpace(a.Machine); m != "" {
		id, err := t.Hub.resolveNode(ctx, m)
		if err != nil {
			return "", err
		}
		nodeID = id
	}
	run, err := t.Hub.Create(ctx, CreateRun{
		ThreadID: tool.TurnFrom(ctx).ThreadID, NodeID: nodeID, Agent: a.Agent, RepoPath: a.RepoPath, Prompt: a.Prompt,
	})
	if err != nil {
		return "", err
	}
	state := "已派发"
	if !t.Hub.Online(run.NodeID) {
		state = "机器离线，已排队，上线后自动开始"
	}
	return fmt.Sprintf("%s：run %s（%s 在机器 %s 上）", state, run.ID, run.Agent, run.NodeID), nil
}

// resolveNode finds a machine by id or (case-insensitive) name.
func (h *Hub) resolveNode(ctx context.Context, ref string) (string, error) {
	nodes, err := h.log.Store.Nodes(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.ID == ref || strings.EqualFold(n.Name, ref) {
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("%w %q", ErrUnknownNode, ref)
}

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

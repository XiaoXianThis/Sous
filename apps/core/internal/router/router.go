// Package router picks the machine and agent for a task run (design §6):
// an explicitly chosen machine, else the project's machines in priority
// order, taking the first that is online, has the agent installed and
// logged in, and has a free run slot.
package router

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// DefaultAgents is the order agents are tried in when nothing names one.
var DefaultAgents = []string{"claude", "codex", "cursor", "opencode", "kiro"}

// ErrNoMachine means no machine can take the run now; the task waits.
var ErrNoMachine = errors.New("no machine can take this task now")

// Node is what routing needs to know about one machine.
type Node struct {
	ID       string
	Name     string
	Online   bool
	Agents   []proto.AgentInfo
	Running  int // queued and running runs
	Capacity int
	Role     string // dev (default) / server
}

type Request struct {
	Kind string // code / general
	// ProjectNodes are where the project lives; code tasks run only there.
	ProjectNodes []event.ProjectNode
	// NodePref is a machine the user or supervisor chose.
	NodePref string
	// Sticky is the machine that holds the task's branch from earlier runs.
	Sticky string
	// Agent is the agent to use; empty picks the first usable one.
	Agent string
	// Role is the machine role the run needs: code and general tasks run on
	// dev machines, ops tasks on servers (M5). Empty: any. A machine named by
	// NodePref or Sticky is used whatever its role.
	Role string
	// Path overrides the project's path on the chosen machine (an ops
	// task's deploy target).
	Path string
}

type Choice struct {
	NodeID string
	Agent  string
	Path   string // the repository on that machine (code tasks)
}

// Route picks a machine. When none fits, the error wraps ErrNoMachine and
// says why, in words for the user.
func Route(req Request, nodes []Node) (Choice, error) {
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	paths := map[string]string{}
	pn := slices.Clone(req.ProjectNodes)
	slices.SortStableFunc(pn, func(a, b event.ProjectNode) int { return a.Priority - b.Priority })
	for _, p := range pn {
		paths[p.NodeID] = p.Path
	}

	var order []string
	switch {
	case req.Sticky != "":
		order = []string{req.Sticky}
	case req.NodePref != "":
		order = []string{req.NodePref}
	case req.Kind == event.TaskKindCode || req.Kind == event.TaskKindOps || len(pn) > 0:
		for _, p := range pn {
			order = append(order, p.NodeID)
		}
	default:
		for _, n := range nodes {
			order = append(order, n.ID)
		}
	}
	if req.Kind == event.TaskKindCode && len(pn) == 0 {
		return Choice{}, fmt.Errorf("%w: 代码任务需要一个登记了机器和路径的项目", ErrNoMachine)
	}
	if req.Kind == event.TaskKindOps && len(pn) == 0 && req.NodePref == "" && req.Sticky == "" {
		return Choice{}, fmt.Errorf("%w: 运维任务需要项目登记部署目标，或者在某台服务器上有目录", ErrNoMachine)
	}
	explicit := req.Sticky != "" || req.NodePref != ""
	if len(order) == 0 {
		return Choice{}, fmt.Errorf("%w: 还没有配对任何机器", ErrNoMachine)
	}

	var reasons []string
	for _, id := range order {
		n, ok := byID[id]
		if !ok {
			reasons = append(reasons, "机器 "+id+" 不存在")
			continue
		}
		path := paths[id]
		if req.Path != "" && explicit {
			path = req.Path
		}
		if (req.Kind == event.TaskKindCode || req.Kind == event.TaskKindOps) && path == "" {
			reasons = append(reasons, n.Name+" 上没有登记这个项目的路径")
			continue
		}
		if req.Role != "" && !explicit && roleOf(n) != req.Role {
			if len(order) == 1 || req.Role == "dev" && roleOf(n) == "server" {
				reasons = append(reasons, n.Name+" 是"+roleName[roleOf(n)]+"，不接"+kindName[req.Kind])
			}
			continue
		}
		if !n.Online {
			reasons = append(reasons, n.Name+" 离线")
			continue
		}
		capacity := n.Capacity
		if capacity <= 0 {
			capacity = 2
		}
		if n.Running >= capacity {
			reasons = append(reasons, n.Name+" 正忙")
			continue
		}
		agent, why := pickAgent(n, req.Agent)
		if agent == "" {
			reasons = append(reasons, n.Name+why)
			continue
		}
		return Choice{NodeID: id, Agent: agent, Path: path}, nil
	}
	return Choice{}, fmt.Errorf("%w: %s", ErrNoMachine, strings.Join(reasons, "；"))
}

func roleOf(n Node) string {
	if n.Role == "server" {
		return "server"
	}
	return "dev"
}

var roleName = map[string]string{"dev": "开发机", "server": "服务器"}
var kindName = map[string]string{event.TaskKindCode: "代码任务", event.TaskKindGeneral: "一般任务", event.TaskKindOps: "运维任务"}

func pickAgent(n Node, want string) (string, string) {
	usable := func(name string) (bool, string) {
		for _, a := range n.Agents {
			if a.Name != name {
				continue
			}
			if !a.Installed {
				return false, " 没有安装 " + name
			}
			if a.LoggedIn != nil && !*a.LoggedIn {
				return false, " 上的 " + name + " 没有登录"
			}
			return true, ""
		}
		return false, " 没有安装 " + name
	}
	if want != "" {
		ok, why := usable(want)
		if ok {
			return want, ""
		}
		return "", why
	}
	for _, name := range DefaultAgents {
		if ok, _ := usable(name); ok {
			return name, ""
		}
	}
	// Agents beyond the built-in list (tests, overrides).
	for _, a := range n.Agents {
		if ok, _ := usable(a.Name); ok {
			return a.Name, ""
		}
	}
	for _, a := range n.Agents {
		if a.Installed {
			return "", " 上的 agent 都没有登录"
		}
	}
	return "", " 没有安装编程 agent"
}

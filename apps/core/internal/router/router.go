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
	// Place is where the task works (decision 0022): worktree and in_place
	// need the project's directory on the machine; scratch runs anywhere.
	Place        string
	ProjectNodes []event.ProjectNode
	NodePref     string // a machine the task names
	Sticky       string // the machine that holds the task's branch or snapshot
	Agent        string
	// Prefer is the machine role tried first (dev / server). It is a
	// preference: machines of the other role are used only when the
	// candidates have none of the preferred role. Naming a machine wins.
	Prefer string
	// Path overrides the project's directory on a named machine (a deploy
	// target's directory).
	Path string
}

type Choice struct {
	NodeID string
	Agent  string
	Path   string // the project's directory on that machine (worktree, in_place)
}

// Route picks the machine and agent for a task's next run.
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
	needsDir := req.Place == event.PlaceWorktree || req.Place == event.PlaceInPlace

	var order []string
	switch {
	case req.Sticky != "":
		order = []string{req.Sticky}
	case req.NodePref != "":
		order = []string{req.NodePref}
	case needsDir || len(pn) > 0:
		for _, p := range pn {
			order = append(order, p.NodeID)
		}
	default:
		for _, n := range nodes {
			order = append(order, n.ID)
		}
	}
	explicit := req.Sticky != "" || req.NodePref != ""
	if needsDir && len(pn) == 0 && !(explicit && req.Path != "") {
		return Choice{}, fmt.Errorf("%w: %s需要一个登记了机器和目录的项目", ErrNoMachine, placeName[req.Place])
	}
	if len(order) == 0 {
		return Choice{}, fmt.Errorf("%w: 还没有配对任何机器", ErrNoMachine)
	}
	if !explicit && req.Prefer != "" {
		// Preferred role first; the others only when no candidate has it.
		var pref, rest []string
		for _, id := range order {
			if n, ok := byID[id]; ok && roleOf(n) == req.Prefer {
				pref = append(pref, id)
			} else {
				rest = append(rest, id)
			}
		}
		if len(pref) > 0 {
			order = pref
		} else {
			order = rest
		}
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
		if needsDir && path == "" {
			reasons = append(reasons, n.Name+" 上没有登记这个项目的目录")
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
		if !needsDir {
			path = ""
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

var placeName = map[string]string{event.PlaceWorktree: "在 worktree 里做的任务", event.PlaceInPlace: "就地执行的任务"}

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

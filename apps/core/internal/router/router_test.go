package router

import (
	"errors"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

func agents(names ...string) []proto.AgentInfo {
	var out []proto.AgentInfo
	for _, n := range names {
		out = append(out, proto.AgentInfo{Name: n, Installed: true})
	}
	return out
}

func TestRouteFollowsProjectPriority(t *testing.T) {
	nodes := []Node{
		{ID: "mac", Name: "Mac", Online: false, Agents: agents("claude")},
		{ID: "pc", Name: "PC", Online: true, Agents: agents("codex")},
		{ID: "box", Name: "Box", Online: true, Agents: agents("claude")},
	}
	req := Request{Place: event.PlaceWorktree, ProjectNodes: []event.ProjectNode{
		{NodeID: "box", Path: "/b", Priority: 3}, {NodeID: "mac", Path: "/m", Priority: 1}, {NodeID: "pc", Path: "/p", Priority: 2},
	}}
	c, err := Route(req, nodes)
	if err != nil || c.NodeID != "pc" || c.Agent != "codex" || c.Path != "/p" {
		t.Fatalf("got %+v %v; want pc (mac is offline)", c, err)
	}

	req.Agent = "claude"
	c, err = Route(req, nodes)
	if err != nil || c.NodeID != "box" {
		t.Fatalf("got %+v %v; want box, the first online machine with claude", c, err)
	}
}

func TestRouteSkipsLoggedOutAndBusy(t *testing.T) {
	no := false
	nodes := []Node{
		{ID: "a", Name: "A", Online: true, Agents: []proto.AgentInfo{{Name: "claude", Installed: true, LoggedIn: &no}}},
		{ID: "b", Name: "B", Online: true, Agents: agents("claude"), Running: 2, Capacity: 2},
		{ID: "c", Name: "C", Online: true, Agents: agents("claude"), Running: 1, Capacity: 2},
	}
	c, err := Route(Request{Place: event.PlaceScratch}, nodes)
	if err != nil || c.NodeID != "c" {
		t.Fatalf("got %+v %v; want c", c, err)
	}
	nodes[2].Running = 2
	_, err = Route(Request{Place: event.PlaceScratch}, nodes)
	if !errors.Is(err, ErrNoMachine) || !strings.Contains(err.Error(), "没有登录") || !strings.Contains(err.Error(), "正忙") {
		t.Fatalf("want a reason per machine, got %v", err)
	}
}

func TestRoutePinnedAndSticky(t *testing.T) {
	nodes := []Node{
		{ID: "a", Name: "A", Online: true, Agents: agents("claude")},
		{ID: "b", Name: "B", Online: false, Agents: agents("claude")},
	}
	pn := []event.ProjectNode{{NodeID: "a", Path: "/a"}, {NodeID: "b", Path: "/b"}}
	if _, err := Route(Request{Place: event.PlaceWorktree, ProjectNodes: pn, NodePref: "b"}, nodes); !errors.Is(err, ErrNoMachine) {
		t.Fatalf("a pinned offline machine must queue, got %v", err)
	}
	if _, err := Route(Request{Place: event.PlaceWorktree, ProjectNodes: pn, Sticky: "b"}, nodes); !errors.Is(err, ErrNoMachine) {
		t.Fatalf("the machine holding the branch must be waited for, got %v", err)
	}
	if _, err := Route(Request{Place: event.PlaceWorktree}, nodes); err == nil || !strings.Contains(err.Error(), "项目") {
		t.Fatalf("a worktree task without a project cannot run, got %v", err)
	}
	if _, err := Route(Request{Place: event.PlaceInPlace}, nodes); err == nil || !strings.Contains(err.Error(), "项目") {
		t.Fatalf("an in-place task without a project cannot run, got %v", err)
	}
	if _, err := Route(Request{Place: event.PlaceScratch}, nil); err == nil {
		t.Fatal("no machines at all must fail")
	}
}

// Machine roles are a preference (decision 0022): the preferred role is
// tried first, the other only when no candidate has the preferred role, and
// naming a machine always wins.
func TestRouteRoles(t *testing.T) {
	yes := true
	agents := []proto.AgentInfo{{Name: "codex", Installed: true, LoggedIn: &yes}}
	nodes := []Node{
		{ID: "srv", Name: "服务器", Online: true, Agents: agents, Capacity: 2, Role: "server"},
		{ID: "mac", Name: "mac", Online: true, Agents: agents, Capacity: 2},
	}
	pn := []event.ProjectNode{{NodeID: "srv", Path: "/srv/app"}, {NodeID: "mac", Path: "/code/app", Priority: 1}}
	c, err := Route(Request{Place: event.PlaceWorktree, ProjectNodes: pn, Prefer: "dev"}, nodes)
	if err != nil || c.NodeID != "mac" || c.Path != "/code/app" {
		t.Fatalf("worktree task: %+v %v", c, err)
	}
	c, err = Route(Request{Place: event.PlaceInPlace, ProjectNodes: pn, Prefer: "server"}, nodes)
	if err != nil || c.NodeID != "srv" || c.Path != "/srv/app" {
		t.Fatalf("ops task: %+v %v", c, err)
	}
	c, err = Route(Request{Place: event.PlaceInPlace, NodePref: "srv", Path: "/srv/deploy", Prefer: "server"}, nodes)
	if err != nil || c.NodeID != "srv" || c.Path != "/srv/deploy" {
		t.Fatalf("deploy target: %+v %v", c, err)
	}
	c, err = Route(Request{Place: event.PlaceScratch, Prefer: "dev"}, nodes)
	if err != nil || c.NodeID != "mac" || c.Path != "" {
		t.Fatalf("scratch task: %+v %v", c, err)
	}
	// In place on a dev machine: allowed, the project is there.
	c, err = Route(Request{Place: event.PlaceInPlace, ProjectNodes: pn, Prefer: "dev"}, nodes)
	if err != nil || c.NodeID != "mac" {
		t.Fatalf("in place on the dev machine: %+v %v", c, err)
	}
	// The preferred dev machine is busy: wait for it, do not move to the server.
	nodes[1].Running = 2
	if _, err := Route(Request{Place: event.PlaceWorktree, ProjectNodes: pn, Prefer: "dev"}, nodes); !errors.Is(err, ErrNoMachine) {
		t.Fatalf("busy dev machine: %v", err)
	}
	// Only a server has the project: it is used.
	nodes[1].Running = 0
	c, err = Route(Request{Place: event.PlaceWorktree, ProjectNodes: pn[:1], Prefer: "dev"}, nodes)
	if err != nil || c.NodeID != "srv" || c.Note != "项目不在任何开发机上，派到了服务器 服务器" {
		t.Fatalf("project only on the server: %+v %v", c, err)
	}
	// An ops task for a project only on the dev machine still runs there
	// (many roads lead to Rome), and says so (decision 0025).
	c, err = Route(Request{Place: event.PlaceInPlace, ProjectNodes: pn[1:], Prefer: "server"}, nodes)
	if err != nil || c.NodeID != "mac" || c.Note != "项目不在任何服务器上，派到了开发机 mac" {
		t.Fatalf("ops task, project only on the dev machine: %+v %v", c, err)
	}
	if c, _ := Route(Request{Place: event.PlaceInPlace, ProjectNodes: pn, Prefer: "server"}, nodes); c.Note != "" {
		t.Fatalf("the preferred machine needs no note: %+v", c)
	}
	// Naming a machine wins over the preference.
	c, err = Route(Request{Place: event.PlaceWorktree, ProjectNodes: pn, NodePref: "srv", Prefer: "dev"}, nodes)
	if err != nil || c.NodeID != "srv" {
		t.Fatalf("named machine: %+v %v", c, err)
	}
}

// Decision 0024: with no agent named, a machine uses its own default, then
// the global default, then any usable agent; disabled agents are never
// picked, even when named.
func TestMachineDefaultsAndDisabledAgents(t *testing.T) {
	mac := Node{ID: "mac", Name: "Mac", Online: true, Agents: []proto.AgentInfo{
		{Name: "claude", Installed: true}, {Name: "codex", Installed: true, Default: true}, {Name: "kiro", Installed: true, Disabled: true},
	}}
	pc := Node{ID: "pc", Name: "PC", Online: true, Agents: []proto.AgentInfo{
		{Name: "claude", Installed: true}, {Name: "opencode", Installed: true},
	}}
	pick := func(n Node, agent, fallback string) (Choice, error) {
		return Route(Request{Place: event.PlaceScratch, NodePref: n.ID, Agent: agent, Fallback: fallback}, []Node{mac, pc})
	}
	if c, err := pick(mac, "", "opencode"); err != nil || c.Agent != "codex" {
		t.Fatalf("the machine's default wins over the global one: %+v %v", c, err)
	}
	if c, err := pick(pc, "", "opencode"); err != nil || c.Agent != "opencode" {
		t.Fatalf("without a machine default the global one is used: %+v %v", c, err)
	}
	if c, err := pick(pc, "", "codex"); err != nil || c.Agent != "claude" {
		t.Fatalf("a global default the machine lacks falls back to any agent: %+v %v", c, err)
	}
	if c, err := pick(mac, "claude", "opencode"); err != nil || c.Agent != "claude" {
		t.Fatalf("a named agent wins: %+v %v", c, err)
	}
	if _, err := pick(mac, "kiro", ""); err == nil || !strings.Contains(err.Error(), "停用") {
		t.Fatalf("a disabled agent was used: %v", err)
	}
	only := Node{ID: "x", Name: "X", Online: true, Agents: []proto.AgentInfo{{Name: "kiro", Installed: true, Disabled: true}}}
	if _, err := Route(Request{Place: event.PlaceScratch}, []Node{only}); err == nil {
		t.Fatal("a machine with only a disabled agent took the run")
	}
	if Usable(mac.Agents, "kiro") || !Usable(mac.Agents, "codex") || Usable(mac.Agents, "gemini") {
		t.Fatal("Usable")
	}
}

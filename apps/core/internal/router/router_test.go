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
	req := Request{Kind: event.TaskKindCode, ProjectNodes: []event.ProjectNode{
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
	c, err := Route(Request{Kind: event.TaskKindGeneral}, nodes)
	if err != nil || c.NodeID != "c" {
		t.Fatalf("got %+v %v; want c", c, err)
	}
	nodes[2].Running = 2
	_, err = Route(Request{Kind: event.TaskKindGeneral}, nodes)
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
	if _, err := Route(Request{Kind: event.TaskKindCode, ProjectNodes: pn, NodePref: "b"}, nodes); !errors.Is(err, ErrNoMachine) {
		t.Fatalf("a pinned offline machine must queue, got %v", err)
	}
	if _, err := Route(Request{Kind: event.TaskKindCode, ProjectNodes: pn, Sticky: "b"}, nodes); !errors.Is(err, ErrNoMachine) {
		t.Fatalf("the machine holding the branch must be waited for, got %v", err)
	}
	if _, err := Route(Request{Kind: event.TaskKindCode}, nodes); err == nil || !strings.Contains(err.Error(), "项目") {
		t.Fatalf("a code task without a project cannot run, got %v", err)
	}
	if _, err := Route(Request{Kind: event.TaskKindGeneral}, nil); err == nil {
		t.Fatal("no machines at all must fail")
	}
}

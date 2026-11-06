package task

import (
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// A machine whose fetch is behind must not send the deploy back to an older
// commit just because it was scanned last (end-to-end review H3).
func TestDeployHeadIsTheNewestCommit(t *testing.T) {
	p := store.Project{ID: "p", Deploy: &event.Deploy{NodeID: "srv", Path: "/srv/app", Branch: "main"},
		Nodes: []event.ProjectNode{{NodeID: "dev", Path: "/code/app"}}}
	scans := []store.RepoScan{
		{NodeID: "srv", Path: "/srv/app", SeenAt: 1000, Heads: map[string]string{"main": "c2"}, HeadTimes: map[string]int64{"main": 200}},
		{NodeID: "dev", Path: "/code/app", SeenAt: 1500, Heads: map[string]string{"main": "c1"}, HeadTimes: map[string]int64{"main": 100}},
	}
	if got := deployHead(p, scans); got != "c2" {
		t.Fatalf("deploy head %q, want the newer commit c2", got)
	}
	scans[1].Heads["main"], scans[1].HeadTimes["main"] = "c3", 300
	if got := deployHead(p, scans); got != "c3" {
		t.Fatalf("deploy head %q, want c3", got)
	}
	// Without commit times (an older daemon), the latest scan decides, but
	// never over a scan that has a time.
	old := []store.RepoScan{
		{NodeID: "srv", Path: "/srv/app", SeenAt: 1000, Heads: map[string]string{"main": "a"}},
		{NodeID: "dev", Path: "/code/app", SeenAt: 1500, Heads: map[string]string{"main": "b"}},
	}
	if got := deployHead(p, old); got != "b" {
		t.Fatalf("deploy head %q, want b", got)
	}
}

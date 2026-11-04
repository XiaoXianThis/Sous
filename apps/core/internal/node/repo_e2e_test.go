package node_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// A project checkout reports where it stands, can be brought up to date
// from the page, and can save its uncommitted work to a branch.
func TestLocationStateSyncAndWIP(t *testing.T) {
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	e := newEnv(t)
	ctx := context.Background()
	seed := gitRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(seed, "clone", "-q", "--bare", seed, origin)
	ws := t.TempDir()
	dir := filepath.Join(ws, "demo")
	other := filepath.Join(t.TempDir(), "other")
	git(seed, "clone", "-q", origin, dir)
	git(seed, "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "up.txt"), []byte("up\n"), 0o644)
	git(other, "add", "-A")
	git(other, "commit", "-qm", "up")
	git(other, "push", "-q")
	e.cfg.Workspaces = []string{ws}
	node.SaveConfig(e.root, e.cfg)
	e.log.Append(ctx, event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{
		ProjectID: "p1", Name: "demo", Nodes: []event.ProjectNode{{NodeID: e.cfg.NodeID, Path: dir}}}))
	defer e.startDaemon("write")()
	call := e.api()
	state := func() *proto.GitState {
		p, _ := e.log.Store.Project(ctx, "p1")
		if len(p.Nodes) == 0 {
			return nil
		}
		return p.Nodes[0].Git
	}
	e.waitFor("the location's git state", func() bool { return state() != nil })
	if st := state(); st.Upstream != "origin/HEAD" && !strings.HasPrefix(st.Upstream, "origin/") {
		t.Fatalf("state %+v", st)
	}

	base := "/api/v1/projects/p1/locations/" + e.cfg.NodeID
	var res proto.RepoResult
	if code := call("POST", base+"/sync", "", &res); code != 200 || res.Outcome != "fast_forwarded" {
		t.Fatalf("sync %d %+v", code, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "up.txt")); err != nil {
		t.Fatal("not fast-forwarded")
	}

	os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("draft\n"), 0o644)
	e.waitFor("the change to show", func() bool {
		call("POST", "/api/v1/nodes/"+e.cfg.NodeID+"/scan", "", nil)
		st := state()
		return st != nil && st.Untracked == 1
	})
	if code := call("POST", base+"/sync", "", &res); code != 200 || res.Outcome != "up_to_date" {
		t.Fatalf("sync again %d %+v", code, res)
	}
	if code := call("POST", base+"/wip", "", &res); code != 200 || res.Outcome != "pushed" || !strings.HasPrefix(res.Branch, "sous/wip/") {
		t.Fatalf("wip %d %+v", code, res)
	}
	if files := git(origin, "ls-tree", "-r", "--name-only", res.Branch); !strings.Contains(files, "draft.txt") {
		t.Fatalf("wip branch: %s", files)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.txt")); err != nil {
		t.Fatal("the draft left the checkout")
	}
	// Both are recorded.
	evs, _ := e.log.Store.EventsSince(ctx, 0, 5000)
	ops := 0
	for _, ev := range evs {
		if ev.Type == event.ProjectRepoOp {
			ops++
		}
	}
	if ops != 3 {
		t.Fatalf("%d repo ops recorded", ops)
	}
}

package node_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
)

// shellProject registers project p1 at a git checkout inside the machine's
// workspace and returns its directory.
func (e *env) shellProject() string {
	e.t.Helper()
	ws := e.t.TempDir()
	dir := filepath.Join(ws, "demo")
	os.MkdirAll(dir, 0o755)
	exec.Command("git", "-C", dir, "init", "-q").Run()
	e.cfg.Workspaces = []string{ws}
	node.SaveConfig(e.root, e.cfg)
	ev := event.New(event.ProjectPut, event.ActorUser, "", event.ProjectPayload{
		ProjectID: "p1", Name: "demo", Nodes: []event.ProjectNode{{NodeID: e.cfg.NodeID, Path: dir}}})
	if _, err := e.log.Append(context.Background(), ev); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

func TestShellInProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	t.Setenv("SHELL", "/bin/sh") // no login profile of the person running the tests
	e := newEnv(t)
	dir := e.shellProject()
	stop := e.startDaemon("write")
	defer stop()
	call := e.api()

	live, unsub := e.log.Bus.Subscribe(1024)
	defer unsub()
	var opened struct {
		NodeID string `json:"node_id"`
		JobID  string `json:"job_id"`
		Root   string `json:"root"`
	}
	if code := call("POST", "/api/v1/projects/p1/shells", `{"node_id":"`+e.cfg.NodeID+`","cols":80,"rows":24}`, &opened); code != 200 || opened.JobID == "" {
		t.Fatalf("open %d %+v", code, opened)
	}
	job := "/api/v1/nodes/" + e.cfg.NodeID + "/jobs/" + opened.JobID
	if code := call("POST", job+"/input", `{"data":"pwd; echo sum-$((40+2))\r"}`, nil); code != 200 {
		t.Fatalf("input %d", code)
	}
	var out strings.Builder
	deadline := time.After(10 * time.Second)
	for !strings.Contains(out.String(), "sum-42") {
		select {
		case ev := <-live:
			if ev.Type == event.AgentJobOutput {
				var p event.AgentJobPayload
				ev.Decode(&p)
				out.WriteString(p.Data)
			}
		case <-deadline:
			t.Fatalf("no shell output, got %q", out.String())
		}
	}
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(out.String(), real) {
		t.Errorf("shell did not start in the project directory %s: %q", real, out.String())
	}

	var list struct {
		Shells []struct {
			NodeID  string `json:"node_id"`
			JobID   string `json:"job_id"`
			Running bool   `json:"running"`
		}
	}
	if call("GET", "/api/v1/projects/p1/shells", "", &list); len(list.Shells) != 1 || list.Shells[0].JobID != opened.JobID || !list.Shells[0].Running {
		t.Fatalf("list %+v", list)
	}

	// Another login session neither sees nor reaches the shell.
	other := e.api()
	var none struct{ Shells []any }
	other("GET", "/api/v1/projects/p1/shells", "", &none)
	if len(none.Shells) != 0 {
		t.Fatalf("another session sees %+v", none)
	}
	if code := other("POST", job+"/input", `{"data":"echo intruder\r"}`, nil); code != 404 {
		t.Fatalf("another session typed into the shell: %d", code)
	}
	if code := other("GET", job, "", nil); code != 404 {
		t.Fatalf("another session read the shell: %d", code)
	}

	// Output is never stored; opening is.
	evs, _ := e.log.Store.EventsSince(context.Background(), 0, 1000)
	opens := 0
	for _, ev := range evs {
		if ev.Type == event.AgentJobOutput {
			t.Fatal("shell output was stored")
		}
		if ev.Type == event.AgentJobStarted {
			opens++
		}
	}
	if opens != 1 {
		t.Fatalf("%d shell.started events", opens)
	}

	call("POST", job+"/cancel", "", nil)
	e.waitFor("the shell to close", func() bool {
		call("GET", "/api/v1/projects/p1/shells", "", &list)
		return len(list.Shells) == 1 && !list.Shells[0].Running
	})
}

func TestShellRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	e := newEnv(t)
	dir := e.shellProject()

	// A directory that is not a checkout is refused.
	os.RemoveAll(filepath.Join(dir, ".git"))
	stop := e.startDaemon("write")
	call := e.api()
	open := `{"node_id":"` + e.cfg.NodeID + `"}`
	if code := call("POST", "/api/v1/projects/p1/shells", open, nil); code != 400 {
		t.Fatalf("shell outside a git checkout: %d", code)
	}
	// A machine the project is not on.
	if code := call("POST", "/api/v1/projects/p1/shells", `{"node_id":"nope"}`, nil); code != 400 {
		t.Fatalf("shell on another machine: %d", code)
	}
	stop()

	// node.json turns shells off.
	exec.Command("git", "-C", dir, "init", "-q").Run()
	off := false
	e.cfg.RemoteShell = &off
	node.SaveConfig(e.root, e.cfg)
	defer e.startDaemon("write")()
	if code := call("POST", "/api/v1/projects/p1/shells", open, nil); code != 403 {
		t.Fatalf("shell with remote_shell off: %d", code)
	}
}

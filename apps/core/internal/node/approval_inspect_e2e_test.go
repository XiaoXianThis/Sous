package node_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

type inspectingApprover struct {
	mu      sync.Mutex
	calls   int
	checked bool
	failure string
}

func (*inspectingApprover) Name() string { return "inspection-fixture" }
func (m *inspectingApprover) Stream(_ context.Context, req provider.Request, _ func(provider.Delta)) (provider.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		if len(req.Tools) != 2 || !strings.Contains(req.Messages[0].PlainText(), "evidence_complete") {
			m.failure = "missing structured evidence/tools"
		}
		call := provider.ToolCall{ID: "read-1", Name: "read_file", Args: json.RawMessage(`{"path":"scripts/make-hello.sh","max_bytes":16384}`)}
		return provider.Response{Message: provider.Message{Role: provider.RoleAssistant, Parts: []provider.Part{{ToolCall: &call}}}}, nil
	}
	for _, part := range req.Messages[len(req.Messages)-1].Parts {
		if r := part.ToolResult; r != nil && !r.IsError && strings.Contains(r.Content, "safe-fixture-marker") && strings.Contains(r.Content, `"complete":true`) {
			m.checked = true
		}
	}
	if !m.checked {
		return provider.Response{}, fmt.Errorf("did not receive complete node inspection: %+v", req.Messages[len(req.Messages)-1])
	}
	return provider.Response{Message: provider.Text(provider.RoleAssistant, `{"decision":"allow","confidence":"high","reason":"已核实安全夹具的全文"}`)}, nil
}

func TestApprovalInspectionRoundTrip(t *testing.T) {
	testApprovalInspectionRoundTrip(t, "write", "#!/bin/sh\nprintf '%s\\n' safe-fixture-marker\n")
}

func TestApprovalInspectionSample09GuardRoundTrip(t *testing.T) {
	// The fake ACP agent never executes this script or keychain commands.
	testApprovalInspectionRoundTrip(t, "approval-guard", "#!/bin/sh\n# safe-fixture-marker\nif [ -z \"${SOUS_SIGN_CERT_P12_PASSWORD:-}\" ]; then exit 1; fi\nsecurity export -k fixture-only\n")
}

func testApprovalInspectionRoundTrip(t *testing.T, mode, script string) {
	t.Helper()
	te := newTaskEnv(t)
	p := filepath.Join(te.repo, "scripts", "make-hello.sh")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "scripts/make-hello.sh"}, {"commit", "-qm", "test fixture"}} {
		c := exec.Command("git", args...)
		c.Dir = te.repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	m := &inspectingApprover{}
	te.tasks.Approver = task.Model{Provider: m, Name: "fixture"}
	defer te.startDaemon(mode)()
	tk, err := te.tasks.Create(context.Background(), te.code(task.Input{Title: "Read safe script before approval", Mode: "auto", Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	if done := te.waitStatus(tk.ID, event.TaskDone, event.TaskBlocked, event.TaskFailed); done.Status != event.TaskDone {
		t.Fatalf("task: %+v", done)
	}
	as, err := te.log.Store.TaskApprovals(context.Background(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 1 || as[0].DecidedBy != event.ActorApprover || !strings.Contains(as[0].Note, "查过：read_file") {
		t.Fatalf("inspection audit missing: %+v", as)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls != 2 || !m.checked || m.failure != "" {
		t.Fatalf("provider calls=%d checked=%t failure=%s", m.calls, m.checked, m.failure)
	}
}

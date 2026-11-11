package httpapi

import (
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
)

// Paired machines (device tokens) get the conversation, never terminal
// output, file or config changes.
func TestDeviceEvents(t *testing.T) {
	for _, ok := range []event.Type{event.MessageUser, event.MessageAssistantDelta, event.ThreadCreated, event.ApprovalRequested,
		event.RunQueued, event.RunEnded, event.TriggerFired} {
		if !deviceEvent(ok) {
			t.Errorf("%s withheld from devices", ok)
		}
	}
	for _, no := range []event.Type{event.AgentJobOutput, event.AgentJobStarted, event.NodeConfigChanged, event.RunOutput,
		event.RunUpdate, event.NodeInventory, event.MemoryPut, event.NotePut} {
		if deviceEvent(no) {
			t.Errorf("%s sent to devices", no)
		}
	}
}

func TestRefsOfPath(t *testing.T) {
	for path, want := range map[string]sysmap.Refs{
		"/api/v1/threads/T1/messages": {ThreadID: "T1"},
		"/api/v1/device/threads/T2":   {ThreadID: "T2"},
		"/api/v1/tasks/K/dispatch":    {TaskID: "K"},
		"/api/v1/runs/R/message":      {RunID: "R"},
		"/api/v1/threads":             {},
		"/api/v1/me":                  {},
	} {
		if got := refsOfPath(path); got != want {
			t.Errorf("refsOfPath(%q) = %+v, want %+v", path, got, want)
		}
	}
}

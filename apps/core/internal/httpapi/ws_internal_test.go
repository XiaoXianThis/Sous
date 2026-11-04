package httpapi

import (
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
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
		event.RunUpdate, event.NodeInventory, event.MemoryPut} {
		if deviceEvent(no) {
			t.Errorf("%s sent to devices", no)
		}
	}
}

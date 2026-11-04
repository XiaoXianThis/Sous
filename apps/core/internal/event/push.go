package event

// Kinds of push notification. The user can turn each off; during quiet
// hours only approvals are sent (requirements R6).
const (
	PushApproval    = "approval"
	PushTaskDone    = "task_done"
	PushTaskFailed  = "task_failed"
	PushTaskBlocked = "task_blocked"
	PushReport      = "report"
	PushInspector   = "inspector"
)

// PushKinds lists every kind, in the order settings show them.
var PushKinds = []string{PushApproval, PushTaskDone, PushTaskFailed, PushTaskBlocked, PushReport, PushInspector}

// PushKind returns the kind of push notification a stored event should
// send, or "" for none.
func PushKind(ev Event) string {
	switch ev.Type {
	case ApprovalRequested:
		var p ApprovalRequestedPayload
		if ev.Decode(&p) == nil && p.Review != "ai" {
			return PushApproval
		}
	case ApprovalEscalated:
		return PushApproval
	case TaskStatus:
		var p TaskStatusPayload
		if ev.Decode(&p) != nil {
			return ""
		}
		switch p.To {
		case TaskDone:
			return PushTaskDone
		case TaskFailed:
			return PushTaskFailed
		case TaskBlocked:
			return PushTaskBlocked
		case TaskQueued:
			if p.Notify {
				return PushTaskBlocked // waiting for a machine: the user may want to switch one on
			}
		}
	case InspectorFinding:
		var p FindingPayload
		if ev.Decode(&p) == nil && p.Action != "none" {
			return PushInspector
		}
	case MessageAssistant:
		var p MessagePayload
		if ev.Decode(&p) == nil && (p.Kind == KindNotice || p.Notify) {
			return PushReport
		}
	}
	return ""
}

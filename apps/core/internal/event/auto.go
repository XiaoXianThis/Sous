package event

// Events of M4: schedules and triggers, the inspector, pausing runs,
// follow-up messages to runs, and data retention.
const (
	SchedulePut     Type = "schedule.put"
	ScheduleDeleted Type = "schedule.deleted"
	// TriggerFired is input for the supervisor that no user typed: a
	// schedule came due, or something needs following up. It is stored as a
	// message of kind "trigger" in its thread, which starts a turn.
	TriggerFired Type = "trigger.fired"

	InspectorFinding Type = "inspector.finding"

	RunPaused  Type = "run.paused"
	RunResumed Type = "run.resumed"
	// RunMessage is a follow-up instruction for a running run; the node
	// reports with RunMessageDelivered once the agent's session has it, so
	// a resumed or retried run gets only what the agent never read.
	RunMessage          Type = "run.message"
	RunMessageDelivered Type = "run.message.delivered"
	// RunArchived: the run's log and diff were compressed into the archive.
	RunArchived Type = "run.archived"

	// NodeUpdateRequested: the user asked a machine to update itself.
	NodeUpdateRequested Type = "node.update_requested"
	NodeUpdateResult    Type = "node.update_result"
)

// Actors of M4.
const (
	ActorTrigger   = "trigger"
	ActorInspector = "inspector"
)

// Message kinds of M4.
const (
	// KindTrigger is a trigger's input to the supervisor (role user).
	KindTrigger = "trigger"
	// KindInspector is the inspector's note about a finding (role assistant).
	KindInspector = "inspector"
)

type SchedulePayload struct {
	ScheduleID string `json:"schedule_id"`
	Title      string `json:"title,omitempty"`
	Cron       string `json:"cron,omitempty"`
	TZ         string `json:"tz,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"` // where the supervisor works on it; empty: the main thread
	Enabled    bool   `json:"enabled"`
}

// Trigger kinds.
const (
	TriggerCron     = "cron"
	TriggerFollowup = "followup"
)

type TriggerPayload struct {
	Kind string `json:"kind"` // cron / followup
	// Key identifies what fired, so the same thing is not followed up twice:
	// schedule:<id>, task:<id>:<status ts>, run:<id>:timeout, approval:<id>,
	// question:<message id>.
	Key        string `json:"key"`
	ScheduleID string `json:"schedule_id,omitempty"`
	MessageID  string `json:"message_id"`
	Title      string `json:"title"`
	// Content is what the supervisor reads, as a user message.
	Content string `json:"content"`
	// At is when the trigger fired, by the scheduler's clock (ms).
	At int64 `json:"at,omitempty"`
}

// Severities and actions of an inspector finding.
const (
	SeverityInfo     = "info"
	SeverityWarn     = "warn"
	SeverityCritical = "critical"

	ActionNotify = "notify"
	ActionPause  = "pause"
	ActionStop   = "stop"
)

type FindingPayload struct {
	FindingID string `json:"finding_id"`
	Rule      string `json:"rule"` // repeat / stalled / long / tokens
	Severity  string `json:"severity"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	// Done says whether the action was carried out.
	Done bool `json:"done"`
}

type RunPausePayload struct {
	Reason string `json:"reason,omitempty"`
	Text   string `json:"text,omitempty"` // resume: what to tell the agent
}

type RunMessagePayload struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

type RunDeliveredPayload struct {
	MessageIDs []string `json:"message_ids"`
}

type NodeUpdatePayload struct {
	NodeID  string `json:"node_id"`
	Version string `json:"version"`
	Error   string `json:"error,omitempty"` // node.update_result
}

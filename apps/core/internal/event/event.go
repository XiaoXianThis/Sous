// Package event defines sous events and the in-memory bus that fans them out.
//
// The events table is the single source of truth; every other table is a view
// projected from it. Ephemeral events (such as streaming deltas) are published
// on the bus only and never stored.
package event

import (
	"encoding/json"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
)

// Type names an event kind.
type Type string

const (
	ThreadCreated         Type = "thread.created"
	MessageUser           Type = "message.user"
	MessageAssistant      Type = "message.assistant"
	MessageAssistantDelta Type = "message.assistant.delta"
	UsageRecorded         Type = "usage.recorded"
	MemoryPut             Type = "memory.put"
	MemoryDeleted         Type = "memory.deleted"
	// ThreadCompacted starts a new context segment of a thread: the
	// messages up to CoversUpTo are folded into a summary (long-main-thread).
	ThreadCompacted Type = "thread.compacted"

	NodePaired    Type = "node.paired"
	NodeUpdated   Type = "node.updated" // renamed
	NodeRevoked   Type = "node.revoked"
	NodeOnline    Type = "node.online"
	NodeOffline   Type = "node.offline"
	NodeInventory Type = "node.inventory"

	RunQueued  Type = "run.queued"
	RunStarted Type = "run.started"
	RunUpdate  Type = "run.update" // tool calls, plans, status changes
	RunOutput  Type = "run.output" // ephemeral text from the agent; also kept in the run log
	RunEnded   Type = "run.ended"
	// RunTurn is one finished turn of a long-lived run (a project
	// consultant): the answer and the agent's session id.
	RunTurn Type = "run.turn"
	// ConsultClosed retires a project's consultant session: a new topic, or
	// the session went on as a task (decision 0022).
	ConsultClosed Type = "consult.closed"

	ApprovalRequested Type = "approval.requested"
	ApprovalDecided   Type = "approval.decided"
	// ApprovalEscalated: the approver agent handed the decision to the user.
	ApprovalEscalated Type = "approval.escalated"
)

// Ephemeral reports whether events of this type are published but never stored.
func (t Type) Ephemeral() bool {
	return t == MessageAssistantDelta || t == RunOutput || t == AgentJobOutput
}

// Actors.
const (
	ActorUser       = "user"
	ActorSupervisor = "supervisor"
	ActorSystem     = "system"
	ActorReviewer   = "reviewer"
	ActorApprover   = "approver"
)

// Event is one immutable fact. Seq is assigned when the event is stored and is
// zero for ephemeral events.
type Event struct {
	Seq      int64           `json:"seq"`
	ID       string          `json:"id"`
	TS       int64           `json:"ts"`
	Type     Type            `json:"type"`
	Actor    string          `json:"actor"`
	ThreadID string          `json:"thread_id,omitempty"`
	TaskID   string          `json:"task_id,omitempty"`
	RunID    string          `json:"run_id,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

// New builds an event with a fresh ID and timestamp. payload is marshalled to JSON.
func New(t Type, actor, threadID string, payload any) Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic("event: marshal payload: " + err.Error())
	}
	return Event{
		ID:       ids.New(),
		TS:       time.Now().UnixMilli(),
		Type:     t,
		Actor:    actor,
		ThreadID: threadID,
		Payload:  raw,
	}
}

// Decode unmarshals the payload into v.
func (e Event) Decode(v any) error {
	return json.Unmarshal(e.Payload, v)
}

// Payloads.

type ThreadCreatedPayload struct {
	Title string `json:"title"`
	Kind  string `json:"kind"` // main / chat / project
	// ProjectID scopes a project chat (kind project) to one project.
	ProjectID string `json:"project_id,omitempty"`
}

// Attachment is a file sent along with a user message; its bytes live in the
// Hub's attachment directory (package attach).
type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

type MessagePayload struct {
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
	Error     bool   `json:"error,omitempty"`
	// ReplyTo is the seq of the newest user message an assistant reply answers.
	ReplyTo int64 `json:"reply_to,omitempty"`
	// Kind is empty for a normal message, "notice" for a proactive
	// notification, or "silent" for a turn that chose not to speak.
	Kind string `json:"kind,omitempty"`
	// Notify asks for a push notification for a report.
	Notify bool `json:"notify,omitempty"`
	// Context is what the user was looking at on the workbench (M5).
	Context *MessageContext `json:"context,omitempty"`
	// Attachments are the files of a user message.
	Attachments []Attachment `json:"attachments,omitempty"`
	// Source names who wrote an assistant message that is not the
	// supervisor's own words: a project consultant (kind "consult").
	Source *MessageSource `json:"source,omitempty"`
}

// MessageSource is the agent behind a consult message.
type MessageSource struct {
	RunID     string `json:"run_id"`
	Agent     string `json:"agent"`
	ProjectID string `json:"project_id"`
	Project   string `json:"project,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
}

// ConsultClosedPayload retires a consultant session.
type ConsultClosedPayload struct {
	ProjectID string `json:"project_id"`
	Reason    string `json:"reason,omitempty"`
}

// Message kinds.
const (
	KindNotice = "notice"
	KindSilent = "silent"
	// KindStep is what the supervisor said before calling tools, stored
	// before the tools run so the conversation keeps the real order: words,
	// then what the tools did (tasks, runs, reports), then the next words.
	// It does not answer the user message; the turn's last message does.
	KindStep = "step"
	// KindConsult is a project consultant's answer, shown as it is.
	KindConsult = "consult"
)

type MemoryPayload struct {
	MemoryID string `json:"memory_id"`
	Kind     string `json:"kind,omitempty"` // profile / project / lesson / note
	Content  string `json:"content,omitempty"`
}

// CompactedPayload starts a context segment. Messages with seq up to
// CoversUpTo are represented by Summary; Profile is the snapshot of the
// user's profile memories the segment shows (formatted, one per item).
type CompactedPayload struct {
	Segment     int      `json:"segment"`
	CoversUpTo  int64    `json:"covers_up_to"`
	Summary     string   `json:"summary"`
	Profile     []string `json:"profile"`
	Reason      string   `json:"reason"` // start / soft / hard / overflow / manual / edit
	InputTokens int      `json:"input_tokens,omitempty"`
	Model       string   `json:"model,omitempty"`
}

// Compaction reasons.
const (
	CompactStart    = "start"
	CompactSoft     = "soft"
	CompactHard     = "hard"
	CompactOverflow = "overflow"
	CompactManual   = "manual"
	CompactEdit     = "edit"
)

type DeltaPayload struct {
	MessageID string `json:"message_id"`
	Delta     string `json:"delta"`
	Thinking  bool   `json:"thinking,omitempty"`
}

type UsagePayload struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Role         string `json:"role"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CachedTokens int    `json:"cached_tokens"`
}

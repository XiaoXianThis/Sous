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
)

// Ephemeral reports whether events of this type are published but never stored.
func (t Type) Ephemeral() bool {
	return t == MessageAssistantDelta
}

// Actors.
const (
	ActorUser       = "user"
	ActorSupervisor = "supervisor"
	ActorSystem     = "system"
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
}

// Message kinds.
const (
	KindNotice = "notice"
	KindSilent = "silent"
)

type MemoryPayload struct {
	MemoryID string `json:"memory_id"`
	Kind     string `json:"kind,omitempty"` // profile / project / lesson / note
	Content  string `json:"content,omitempty"`
}

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

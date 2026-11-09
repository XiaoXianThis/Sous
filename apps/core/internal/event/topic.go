package event

// Topics (work/specs/chat-topics): a thread's messages are grouped into
// topics. A topic starts with the message that has no topic (its id is that
// message's id); these events change it afterwards.
const (
	// TopicUpdated renames a topic, sets its current conclusion, or closes
	// and reopens it.
	TopicUpdated Type = "topic.updated"
	// MessageMoved puts a message into another topic: the supervisor
	// attaching a follow-up to the topic it is working on, or the user
	// taking it back out.
	MessageMoved Type = "message.moved"
	// MessageSeen says the supervisor has started on messages (ephemeral).
	MessageSeen Type = "message.seen"
)

// Topic states.
const (
	TopicOpen   = "open"
	TopicClosed = "closed"
)

type TopicUpdatedPayload struct {
	TopicID string  `json:"topic_id"`
	Title   *string `json:"title,omitempty"`
	// Summary is the topic's current conclusion: where it stands, in a
	// sentence or two.
	Summary *string `json:"summary,omitempty"`
	Status  *string `json:"status,omitempty"`
}

type MessageMovedPayload struct {
	MessageID string `json:"message_id"`
	TopicID   string `json:"topic_id"`
	From      string `json:"from"`
}

type MessageSeenPayload struct {
	MessageIDs []string `json:"message_ids"`
}

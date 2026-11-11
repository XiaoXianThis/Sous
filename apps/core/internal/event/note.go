package event

const (
	NotePut     Type = "note.put"
	NoteDeleted Type = "note.deleted"
	NoteSent    Type = "note.sent"
)
const (
	NoteOpen     = "open"
	NoteSentDone = "sent"
	NoteArchived = "archived"
)

type NotePayload struct {
	NoteID      string       `json:"note_id"`
	Text        *string      `json:"text,omitempty"`
	ProjectID   *string      `json:"project_id,omitempty"`
	Status      string       `json:"status,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}
type NoteSentPayload struct {
	NoteIDs   []string `json:"note_ids"`
	ThreadID  string   `json:"thread_id"`
	TopicID   string   `json:"topic_id"`
	MessageID string   `json:"message_id"`
}

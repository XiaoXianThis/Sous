package event

const (
	MeetingClosed   Type = "meeting.closed"
	MeetingReopened Type = "meeting.reopened"
	MeetingHostSet  Type = "meeting.host"
	MinutesDrafted  Type = "minutes.drafted"
	MinutesPosted   Type = "minutes.posted"
)
const (
	KindMeeting         = "meeting"
	KindMinutes         = "minutes"
	KindMeetingProposal = "meeting_proposal"
	TriggerMeeting      = "meeting"
	MeetingOpen         = "open"
	MeetingClosedState  = "closed"
)

type MeetingHost struct {
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
}
type MeetingPayload struct {
	MeetingID string       `json:"meeting_id"`
	Host      *MeetingHost `json:"host,omitempty"`
}
type MinutesPayload struct {
	MeetingID string `json:"meeting_id"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	MessageID string `json:"message_id,omitempty"`
}

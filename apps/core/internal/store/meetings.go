package store

import (
	"context"
	"encoding/json"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

type MinutesRow struct {
	MeetingID string `json:"meeting_id"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	MessageID string `json:"message_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
	PostedAt  int64  `json:"posted_at,omitempty"`
}

func projectMeeting(w *Writer, ev event.Event) error {
	switch ev.Type {
	case event.MeetingClosed, event.MeetingReopened:
		status := event.MeetingOpen
		if ev.Type == event.MeetingClosed {
			status = event.MeetingClosedState
		}
		_, err := w.tx.ExecContext(w.ctx, `UPDATE threads SET meeting_status=?,updated_at=? WHERE id=?`, status, ev.TS, ev.ThreadID)
		return err
	case event.MeetingHostSet:
		var p event.MeetingPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		host := ""
		if p.Host != nil {
			raw, err := json.Marshal(p.Host)
			if err != nil {
				return err
			}
			host = string(raw)
		}
		_, err := w.tx.ExecContext(w.ctx, `UPDATE threads SET host=?,updated_at=? WHERE id=?`, host, ev.TS, ev.ThreadID)
		return err
	case event.MinutesDrafted, event.MinutesPosted:
		var p event.MinutesPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if ev.Type == event.MinutesDrafted {
			_, err := w.tx.ExecContext(w.ctx, `INSERT OR REPLACE INTO minutes(meeting_id,version,content,status,created_at) VALUES(?,?,?,'draft',?)`, p.MeetingID, p.Version, p.Content, ev.TS)
			return err
		}
		_, err := w.tx.ExecContext(w.ctx, `UPDATE minutes SET status='posted',content=?,message_id=?,posted_at=? WHERE meeting_id=? AND version=?`, p.Content, p.MessageID, ev.TS, p.MeetingID, p.Version)
		return err
	}
	return nil
}
func (s *Store) Meetings(ctx context.Context, parent string) ([]Thread, error) {
	if parent == "" {
		return s.threads(ctx, `WHERE kind='meeting' ORDER BY updated_at DESC LIMIT 200`)
	}
	return s.threads(ctx, `WHERE kind='meeting' AND parent_thread_id=? ORDER BY updated_at DESC LIMIT 200`, parent)
}
func (s *Store) Minutes(ctx context.Context, id string) ([]MinutesRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT meeting_id,version,content,status,message_id,created_at,posted_at FROM minutes WHERE meeting_id=? ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MinutesRow{}
	for rows.Next() {
		var m MinutesRow
		if err := rows.Scan(&m.MeetingID, &m.Version, &m.Content, &m.Status, &m.MessageID, &m.CreatedAt, &m.PostedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) LinkedMeeting(ctx context.Context, threadID, meetingID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE thread_id=? AND kind='minutes' AND json_valid(source) AND json_extract(source,'$.meeting_id')=?`, threadID, meetingID).Scan(&count)
	return count > 0, err
}

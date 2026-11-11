package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

type Note struct {
	ID          string             `json:"id"`
	Text        string             `json:"text"`
	ProjectID   string             `json:"project_id,omitempty"`
	Status      string             `json:"status"`
	Attachments []event.Attachment `json:"attachments,omitempty"`
	Sends       []NoteSend         `json:"sends,omitempty"`
	CreatedAt   int64              `json:"created_at"`
	UpdatedAt   int64              `json:"updated_at"`
}
type NoteSend struct {
	ThreadID  string `json:"thread_id"`
	TopicID   string `json:"topic_id"`
	MessageID string `json:"message_id"`
	At        int64  `json:"at"`
}
type NoteFilter struct{ Status, ProjectID, Query string }

func projectNote(w *Writer, ev event.Event) error {
	switch ev.Type {
	case event.NotePut:
		var p event.NotePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		text, project, status := "", "", p.Status
		if p.Text != nil {
			text = *p.Text
		}
		if p.ProjectID != nil {
			project = *p.ProjectID
		}
		if status == "" {
			status = event.NoteOpen
		}
		attachments := ""
		if len(p.Attachments) > 0 {
			raw, err := json.Marshal(p.Attachments)
			if err != nil {
				return err
			}
			attachments = string(raw)
		}
		_, err := w.tx.ExecContext(w.ctx, `INSERT INTO notes(id,text,project_id,status,attachments,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET text=CASE WHEN ? THEN excluded.text ELSE notes.text END, project_id=CASE WHEN ? THEN excluded.project_id ELSE notes.project_id END, status=CASE WHEN ? THEN excluded.status ELSE notes.status END, updated_at=excluded.updated_at`, p.NoteID, text, project, status, attachments, ev.TS, ev.TS, p.Text != nil, p.ProjectID != nil, p.Status != "")
		return err
	case event.NoteDeleted:
		var p event.NotePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM note_sends WHERE note_id=?`, p.NoteID); err != nil {
			return err
		}
		_, err := w.tx.ExecContext(w.ctx, `DELETE FROM notes WHERE id=?`, p.NoteID)
		return err
	case event.NoteSent:
		var p event.NoteSentPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		for _, id := range p.NoteIDs {
			if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO note_sends(note_id,thread_id,topic_id,message_id,at) VALUES(?,?,?,?,?)`, id, p.ThreadID, p.TopicID, p.MessageID, ev.TS); err != nil {
				return err
			}
			if _, err := w.tx.ExecContext(w.ctx, `UPDATE notes SET status='sent',updated_at=? WHERE id=? AND status='open'`, ev.TS, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func scanNote(row interface{ Scan(...any) error }) (Note, error) {
	var n Note
	var attachments string
	err := row.Scan(&n.ID, &n.Text, &n.ProjectID, &n.Status, &attachments, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, err
	}
	if attachments != "" {
		if err := json.Unmarshal([]byte(attachments), &n.Attachments); err != nil {
			return n, err
		}
	}
	return n, nil
}
func (s *Store) noteSends(ctx context.Context, n *Note) error {
	rows, err := s.db.QueryContext(ctx, `SELECT thread_id,topic_id,message_id,at FROM note_sends WHERE note_id=? ORDER BY rowid`, n.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v NoteSend
		if err := rows.Scan(&v.ThreadID, &v.TopicID, &v.MessageID, &v.At); err != nil {
			return err
		}
		n.Sends = append(n.Sends, v)
	}
	return rows.Err()
}
func (s *Store) Note(ctx context.Context, id string) (Note, error) {
	n, err := scanNote(s.db.QueryRowContext(ctx, `SELECT id,text,project_id,status,attachments,created_at,updated_at FROM notes WHERE id=?`, id))
	if err != nil {
		return n, err
	}
	err = s.noteSends(ctx, &n)
	return n, err
}
func (s *Store) Notes(ctx context.Context, f NoteFilter) ([]Note, error) {
	query := `SELECT id,text,project_id,status,attachments,created_at,updated_at FROM notes WHERE 1=1`
	args := []any{}
	if f.Status != "" {
		query += ` AND status=?`
		args = append(args, f.Status)
	}
	if f.ProjectID != "" {
		query += ` AND project_id=?`
		args = append(args, f.ProjectID)
	}
	if f.Query != "" {
		query += ` AND text LIKE ? ESCAPE '\'`
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)
		args = append(args, "%"+escaped+"%")
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY updated_at DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	notes := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		notes = append(notes, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range notes {
		if err := s.noteSends(ctx, &notes[i]); err != nil {
			return nil, err
		}
	}
	return notes, nil
}

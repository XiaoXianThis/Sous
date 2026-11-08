package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// EventsSince returns stored events with seq > since, oldest first.
func (s *Store) EventsSince(ctx context.Context, since int64, limit int) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, id, ts, type, actor, IFNULL(thread_id,''), IFNULL(task_id,''), IFNULL(run_id,''), payload
		 FROM events WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event.Event
	for rows.Next() {
		var ev event.Event
		var typ, payload string
		if err := rows.Scan(&ev.Seq, &ev.ID, &ev.TS, &typ, &ev.Actor, &ev.ThreadID, &ev.TaskID, &ev.RunID, &payload); err != nil {
			return nil, err
		}
		ev.Type = event.Type(typ)
		ev.Payload = []byte(payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// LastSeq returns the newest stored seq, or 0.
func (s *Store) LastSeq(ctx context.Context) (int64, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&seq)
	return seq.Int64, err
}

type Thread struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	ProjectID string `json:"project_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Archived  bool   `json:"archived"`
}

// Threads lists the general threads (not project chats), the main thread
// first, then most recently updated.
func (s *Store) Threads(ctx context.Context) ([]Thread, error) {
	return s.threads(ctx, `WHERE archived = 0 AND project_id = '' ORDER BY kind = 'main' DESC, updated_at DESC`)
}

// ProjectThreads lists a project's chats, most recently updated first.
func (s *Store) ProjectThreads(ctx context.Context, projectID string) ([]Thread, error) {
	return s.threads(ctx, `WHERE archived = 0 AND project_id = ? ORDER BY updated_at DESC`, projectID)
}

// AllThreads lists every open thread, project chats included.
func (s *Store) AllThreads(ctx context.Context) ([]Thread, error) {
	return s.threads(ctx, `WHERE archived = 0 ORDER BY updated_at DESC`)
}

const threadCols = `id, title, kind, project_id, created_at, updated_at, archived`

func (s *Store) threads(ctx context.Context, where string, args ...any) ([]Thread, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+threadCols+` FROM threads `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Thread{}
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.Title, &t.Kind, &t.ProjectID, &t.CreatedAt, &t.UpdatedAt, &t.Archived); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Thread(ctx context.Context, id string) (Thread, error) {
	var t Thread
	err := s.db.QueryRowContext(ctx,
		`SELECT `+threadCols+` FROM threads WHERE id = ?`, id).
		Scan(&t.ID, &t.Title, &t.Kind, &t.ProjectID, &t.CreatedAt, &t.UpdatedAt, &t.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// MainThreadID returns the main thread's id, or ErrNotFound.
func (s *Store) MainThreadID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM threads WHERE kind = 'main'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

type Message struct {
	ID        string `json:"id"`
	ThreadID  string `json:"thread_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Error     bool   `json:"error,omitempty"`
	ReplyTo   int64  `json:"reply_to,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Seq       int64  `json:"seq"`
	CreatedAt int64  `json:"created_at"`
	// Context is what the user was looking at on the workbench (M5).
	Context *event.MessageContext `json:"context,omitempty"`
	// Attachments are the files sent with a user message.
	Attachments []event.Attachment `json:"attachments,omitempty"`
	// Source names the consultant behind a consult message.
	Source *event.MessageSource `json:"source,omitempty"`
	// Tools are the supervisor's own tool calls after this message.
	Tools []event.ToolNote `json:"tools,omitempty"`
}

// Messages returns up to limit messages of a thread with seq < before (0 means
// newest), oldest first.
func (s *Store) Messages(ctx context.Context, threadID string, before int64, limit int) ([]Message, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, role, content, error, reply_to, kind, event_seq, created_at, context, attachments, source, tools FROM (
		   SELECT * FROM messages WHERE thread_id = ? AND event_seq < ? ORDER BY event_seq DESC LIMIT ?
		 ) ORDER BY event_seq`, threadID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessageRows(rows)
}

func scanMessageRows(rows *sql.Rows) ([]Message, error) {
	out := []Message{}
	for rows.Next() {
		var m Message
		var mctx, atts, source, tools string
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Role, &m.Content, &m.Error, &m.ReplyTo, &m.Kind, &m.Seq, &m.CreatedAt, &mctx, &atts, &source, &tools); err != nil {
			return nil, err
		}
		if tools != "" {
			_ = json.Unmarshal([]byte(tools), &m.Tools)
		}
		if source != "" {
			m.Source = &event.MessageSource{}
			if json.Unmarshal([]byte(source), m.Source) != nil {
				m.Source = nil
			}
		}
		if atts != "" {
			_ = json.Unmarshal([]byte(atts), &m.Attachments)
		}
		if mctx != "" {
			var c event.MessageContext
			if json.Unmarshal([]byte(mctx), &c) == nil {
				m.Context = &c
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type UsageRow struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Role         string `json:"role"`
	Calls        int    `json:"calls"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CachedTokens int    `json:"cached_tokens"`
}

// UsageSince sums token usage since t, grouped by provider, model and role.
func (s *Store) UsageSince(ctx context.Context, t time.Time) ([]UsageRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT provider, model, role, COUNT(*), SUM(input_tokens), SUM(output_tokens), SUM(cached_tokens)
		 FROM usage WHERE ts >= ? GROUP BY provider, model, role ORDER BY provider, model, role`, t.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var u UsageRow
		if err := rows.Scan(&u.Provider, &u.Model, &u.Role, &u.Calls, &u.InputTokens, &u.OutputTokens, &u.CachedTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Setting reads a value from the settings table.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting writes a value to the settings table. Settings are local state,
// not domain events, so they bypass the event log.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

// UsageProject is token usage of one project ("" for the main conversation
// and anything not tied to a project).
type UsageProject struct {
	ProjectID    string `json:"project_id"`
	Name         string `json:"name"`
	Calls        int    `json:"calls"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CachedTokens int    `json:"cached_tokens"`
}

// UsageByProject sums token usage since t by project: a call of a task
// counts for the task's project, a call in a project chat for that project
// (end-to-end review 4.22).
func (s *Store) UsageByProject(ctx context.Context, t time.Time) ([]UsageProject, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pid, COALESCE((SELECT name FROM projects WHERE id = pid), ''), COUNT(*), SUM(i), SUM(o), SUM(c) FROM (
			SELECT COALESCE(NULLIF(k.project_id, ''), NULLIF(th.project_id, ''), '') AS pid,
				u.input_tokens AS i, u.output_tokens AS o, u.cached_tokens AS c
			FROM usage u
			LEFT JOIN tasks k ON k.id = u.task_id
			LEFT JOIN threads th ON th.id = u.thread_id
			WHERE u.ts >= ?)
		 GROUP BY pid ORDER BY SUM(i) + SUM(o) DESC`, t.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageProject{}
	for rows.Next() {
		var u UsageProject
		if err := rows.Scan(&u.ProjectID, &u.Name, &u.Calls, &u.InputTokens, &u.OutputTokens, &u.CachedTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

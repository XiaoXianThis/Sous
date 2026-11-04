package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// projectAuto projects schedules, triggers and inspector findings (M4).
func projectAuto(w *Writer, ev event.Event) error {
	exec := func(q string, args ...any) error {
		_, err := w.tx.ExecContext(w.ctx, q, args...)
		return err
	}
	switch ev.Type {
	case event.SchedulePut:
		var p event.SchedulePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`INSERT INTO schedules(id, title, cron, tz, prompt, thread_id, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET title = excluded.title, cron = excluded.cron, tz = excluded.tz, prompt = excluded.prompt,
			thread_id = excluded.thread_id, enabled = excluded.enabled, updated_at = excluded.updated_at`,
			p.ScheduleID, p.Title, p.Cron, p.TZ, p.Prompt, p.ThreadID, p.Enabled, ev.TS, ev.TS)
	case event.ScheduleDeleted:
		var p event.SchedulePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`DELETE FROM schedules WHERE id = ?`, p.ScheduleID)
	case event.TriggerFired:
		var p event.TriggerPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		at := p.At
		if at == 0 {
			at = ev.TS
		}
		if err := exec(`INSERT INTO trigger_state(key, last_fired) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET last_fired = excluded.last_fired`,
			p.Key, at); err != nil {
			return err
		}
		if p.ScheduleID != "" {
			if err := exec(`UPDATE schedules SET last_run = ? WHERE id = ?`, at, p.ScheduleID); err != nil {
				return err
			}
		}
		// The supervisor reads it like a user message of its thread.
		if err := exec(`INSERT INTO messages(id, thread_id, role, content, kind, event_seq, created_at) VALUES (?,?,?,?,?,?,?)`,
			p.MessageID, ev.ThreadID, "user", p.Content, event.KindTrigger, ev.Seq, ev.TS); err != nil {
			return err
		}
		return exec(`UPDATE threads SET updated_at = ? WHERE id = ?`, ev.TS, ev.ThreadID)
	case event.InspectorFinding:
		var p event.FindingPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`INSERT INTO findings(id, run_id, task_id, rule, severity, action, reason, done, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			p.FindingID, ev.RunID, ev.TaskID, p.Rule, p.Severity, p.Action, p.Reason, p.Done, ev.TS)
	}
	return nil
}

type Schedule struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Cron      string `json:"cron"`
	TZ        string `json:"tz"`
	Prompt    string `json:"prompt"`
	ThreadID  string `json:"thread_id"`
	Enabled   bool   `json:"enabled"`
	LastRun   int64  `json:"last_run"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	// NextRun is filled in by the trigger package, not stored.
	NextRun int64 `json:"next_run"`
}

// Schedules lists the schedules, oldest first.
func (s *Store) Schedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, cron, tz, prompt, thread_id, enabled, last_run, created_at, updated_at FROM schedules ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var x Schedule
		if err := rows.Scan(&x.ID, &x.Title, &x.Cron, &x.TZ, &x.Prompt, &x.ThreadID, &x.Enabled, &x.LastRun, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) Schedule(ctx context.Context, id string) (Schedule, error) {
	all, err := s.Schedules(ctx)
	if err != nil {
		return Schedule{}, err
	}
	for _, x := range all {
		if x.ID == id {
			return x, nil
		}
	}
	return Schedule{}, ErrNotFound
}

// TriggerFiredAt returns when a trigger key last fired, or 0.
func (s *Store) TriggerFiredAt(ctx context.Context, key string) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx, `SELECT last_fired FROM trigger_state WHERE key = ?`, key).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return ts, err
}

type Finding struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	TaskID    string `json:"task_id"`
	Rule      string `json:"rule"`
	Severity  string `json:"severity"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	Done      bool   `json:"done"`
	CreatedAt int64  `json:"created_at"`
}

// Findings lists the newest findings first; runID filters when set.
func (s *Store) Findings(ctx context.Context, runID string, limit int) ([]Finding, error) {
	q := `SELECT id, run_id, task_id, rule, severity, action, reason, done, created_at FROM findings`
	var args []any
	if runID != "" {
		q += ` WHERE run_id = ?`
		args = append(args, runID)
	}
	if limit <= 0 {
		limit = 100
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.RunID, &f.TaskID, &f.Rule, &f.Severity, &f.Action, &f.Reason, &f.Done, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type RunMessage struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	Text      string `json:"text"`
	Delivered bool   `json:"delivered"`
	CreatedAt int64  `json:"created_at"`
}

// RunMessages lists a run's follow-up messages, oldest first.
func (s *Store) RunMessages(ctx context.Context, runID string) ([]RunMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, text, delivered, created_at FROM run_messages WHERE run_id = ? ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunMessage{}
	for rows.Next() {
		var m RunMessage
		if err := rows.Scan(&m.ID, &m.RunID, &m.Text, &m.Delivered, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecentRunEvents returns the newest n stored events of a run, oldest first.
func (s *Store) RecentRunEvents(ctx context.Context, runID string, n int) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, id, ts, type, actor, IFNULL(thread_id,''), IFNULL(task_id,''), IFNULL(run_id,''), payload FROM (
		   SELECT * FROM events WHERE run_id = ? ORDER BY seq DESC LIMIT ?
		 ) ORDER BY seq`, runID, n)
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

type UsageDay struct {
	Day          string `json:"day"` // YYYY-MM-DD in loc
	Role         string `json:"role"`
	Calls        int    `json:"calls"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CachedTokens int    `json:"cached_tokens"`
}

// UsageDaily sums token usage since t per day (in loc) and role, oldest first.
func (s *Store) UsageDaily(ctx context.Context, t time.Time, loc *time.Location) ([]UsageDay, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, role, input_tokens, output_tokens, cached_tokens FROM usage WHERE ts >= ? ORDER BY ts`, t.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[[2]string]int{}
	out := []UsageDay{}
	for rows.Next() {
		var ts int64
		var role string
		var in, outT, cached int
		if err := rows.Scan(&ts, &role, &in, &outT, &cached); err != nil {
			return nil, err
		}
		day := time.UnixMilli(ts).In(loc).Format("2006-01-02")
		k := [2]string{day, role}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, UsageDay{Day: day, Role: role})
		}
		out[i].Calls++
		out[i].InputTokens += in
		out[i].OutputTokens += outT
		out[i].CachedTokens += cached
	}
	return out, rows.Err()
}

// TokensSince sums input and output tokens since t.
func (s *Store) TokensSince(ctx context.Context, t time.Time) (int, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(input_tokens + output_tokens) FROM usage WHERE ts >= ?`, t.UnixMilli()).Scan(&n)
	return int(n.Int64), err
}

// ArchivableRuns lists runs not archived yet whose task closed before t.
func (s *Store) ArchivableRuns(ctx context.Context, t time.Time, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixed("r.", runCols)+` FROM runs r JOIN tasks k ON k.id = r.task_id
		WHERE r.archived = 0 AND r.status NOT IN ('queued', 'running') AND k.status IN ('done', 'cancelled') AND k.updated_at < ?
		ORDER BY r.created_at LIMIT ?`, t.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	return scanRuns(rows)
}

func prefixed(p, cols string) string {
	out := ""
	for i, c := range splitCols(cols) {
		if i > 0 {
			out += ", "
		}
		out += p + c
	}
	return out
}

func splitCols(cols string) []string {
	var out []string
	cur := ""
	for _, r := range cols {
		switch r {
		case ',':
			out = append(out, cur)
			cur = ""
		case ' ', '\n', '\t':
		default:
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

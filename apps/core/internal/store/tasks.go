package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// Effect kinds enqueued by projections.
const (
	EffectTaskAdvance = "task.advance" // a task's run ended: review, deliver or report
	EffectApprove     = "approve"      // the approver agent decides an approval
	EffectPush        = "push"         // send a push notification
)

// enqueueEffects schedules the side effects of a stored event, in the same
// transaction, so they survive a crash between commit and execution.
func enqueueEffects(w *Writer, ev event.Event) error {
	if kind := event.PushKind(ev); kind != "" {
		raw, _ := json.Marshal(ev)
		if err := w.Enqueue(EffectPush, ev.ID, raw); err != nil {
			return err
		}
	}
	switch ev.Type {
	case event.RunEnded:
		var task string
		err := w.tx.QueryRowContext(w.ctx, `SELECT task_id FROM runs WHERE id = ?`, ev.RunID).Scan(&task)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if task != "" {
			return w.Enqueue(EffectTaskAdvance, ev.ID, []byte(ev.RunID))
		}
	case event.ApprovalRequested:
		var p event.ApprovalRequestedPayload
		if ev.Decode(&p) == nil && p.Review == "ai" {
			return w.Enqueue(EffectApprove, p.ApprovalID, nil)
		}
	}
	return nil
}

func projectTask(w *Writer, ev event.Event) error {
	exec := func(q string, args ...any) error {
		_, err := w.tx.ExecContext(w.ctx, q, args...)
		return err
	}
	switch ev.Type {
	case event.TaskCreated:
		var p event.TaskCreatedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if err := exec(`INSERT INTO tasks(id, parent_id, origin_thread_id, kind, title, detail, status, priority, project_id,
			agent_pref, node_pref, permission_mode, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			ev.TaskID, p.ParentID, ev.ThreadID, p.Kind, p.Title, p.Detail, p.Status, p.Priority, p.ProjectID,
			p.AgentPref, p.NodePref, p.PermissionMode, ev.TS, ev.TS); err != nil {
			return err
		}
		return setDeps(w, ev.TaskID, p.DependsOn)

	case event.TaskUpdated:
		var p event.TaskUpdatedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		sets := []string{"updated_at = ?"}
		args := []any{ev.TS}
		add := func(col string, v any) {
			sets = append(sets, col+" = ?")
			args = append(args, v)
		}
		if p.Title != nil {
			add("title", *p.Title)
		}
		if p.Detail != nil {
			add("detail", *p.Detail)
		}
		if p.Priority != nil {
			add("priority", *p.Priority)
		}
		if p.ProjectID != nil {
			add("project_id", *p.ProjectID)
		}
		if p.AgentPref != nil {
			add("agent_pref", *p.AgentPref)
		}
		if p.NodePref != nil {
			add("node_pref", *p.NodePref)
		}
		if p.PermissionMode != nil {
			add("permission_mode", *p.PermissionMode)
		}
		if p.Dismissed != nil {
			add("dismissed", *p.Dismissed)
		}
		args = append(args, ev.TaskID)
		if err := exec(`UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
		if p.SetDeps || len(p.DependsOn) > 0 {
			return setDeps(w, ev.TaskID, p.DependsOn)
		}
		return nil

	case event.TaskStatus:
		var p event.TaskStatusPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE tasks SET status = ?, status_reason = ?, next = ?, pr_url = CASE WHEN ? = '' THEN pr_url ELSE ? END,
			dismissed = 0, updated_at = ? WHERE id = ?`, p.To, p.Reason, p.Next, p.PRURL, p.PRURL, ev.TS, ev.TaskID)

	case event.TaskReviewed:
		var p event.TaskReviewedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE tasks SET review_note = ?, updated_at = ? WHERE id = ?`, p.Reason, ev.TS, ev.TaskID)

	case event.ProjectPut:
		var p event.ProjectPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if err := exec(`INSERT INTO projects(id, name, repo_url, default_mode, default_agent, created_at, updated_at) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, repo_url = excluded.repo_url, default_mode = excluded.default_mode,
			default_agent = excluded.default_agent, updated_at = excluded.updated_at`,
			p.ProjectID, p.Name, p.RepoURL, p.DefaultMode, p.DefaultAgent, ev.TS, ev.TS); err != nil {
			return err
		}
		if err := exec(`DELETE FROM project_nodes WHERE project_id = ?`, p.ProjectID); err != nil {
			return err
		}
		for _, n := range p.Nodes {
			if err := exec(`INSERT OR REPLACE INTO project_nodes(project_id, node_id, path, priority) VALUES (?,?,?,?)`,
				p.ProjectID, n.NodeID, n.Path, n.Priority); err != nil {
				return err
			}
		}
		return nil

	case event.ProjectDeleted:
		var p event.ProjectPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`DELETE FROM projects WHERE id = ?`, p.ProjectID)
	}
	return nil
}

func setDeps(w *Writer, taskID string, deps []string) error {
	if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM task_deps WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	for _, d := range deps {
		if d == "" || d == taskID {
			continue
		}
		if _, err := w.tx.ExecContext(w.ctx, `INSERT OR IGNORE INTO task_deps(task_id, depends_on) VALUES (?,?)`, taskID, d); err != nil {
			return err
		}
	}
	return nil
}

type Task struct {
	ID             string   `json:"id"`
	ParentID       string   `json:"parent_id"`
	OriginThreadID string   `json:"origin_thread_id"`
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	Detail         string   `json:"detail"`
	Status         string   `json:"status"`
	StatusReason   string   `json:"status_reason"`
	Priority       int      `json:"priority"`
	ProjectID      string   `json:"project_id"`
	AgentPref      string   `json:"agent_pref"`
	NodePref       string   `json:"node_pref"`
	PermissionMode string   `json:"permission_mode"`
	Branch         string   `json:"branch"`
	BaseBranch     string   `json:"base_branch"`
	PRURL          string   `json:"pr_url"`
	ReviewNote     string   `json:"review_note"`
	Next           string   `json:"next"`
	Dismissed      bool     `json:"dismissed"`
	DependsOn      []string `json:"depends_on"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

const taskCols = `id, parent_id, origin_thread_id, kind, title, detail, status, status_reason, priority, project_id,
	agent_pref, node_pref, permission_mode, branch, base_branch, pr_url, review_note, next, dismissed, created_at, updated_at`

func (s *Store) scanTasks(ctx context.Context, rows *sql.Rows) ([]Task, error) {
	out := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ParentID, &t.OriginThreadID, &t.Kind, &t.Title, &t.Detail, &t.Status, &t.StatusReason,
			&t.Priority, &t.ProjectID, &t.AgentPref, &t.NodePref, &t.PermissionMode, &t.Branch, &t.BaseBranch, &t.PRURL,
			&t.ReviewNote, &t.Next, &t.Dismissed, &t.CreatedAt, &t.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		t.DependsOn = []string{}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	idx := map[string]int{}
	ids := make([]any, len(out))
	for i, t := range out {
		idx[t.ID] = i
		ids[i] = t.ID
	}
	deps, err := s.db.QueryContext(ctx,
		`SELECT task_id, depends_on FROM task_deps WHERE task_id IN (?`+repeat(",?", len(ids)-1)+`) ORDER BY rowid`, ids...)
	if err != nil {
		return nil, err
	}
	defer deps.Close()
	for deps.Next() {
		var t, d string
		if err := deps.Scan(&t, &d); err != nil {
			return nil, err
		}
		out[idx[t]].DependsOn = append(out[idx[t]].DependsOn, d)
	}
	return out, deps.Err()
}

func (s *Store) Task(ctx context.Context, id string) (Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id = ?`, id)
	if err != nil {
		return Task{}, err
	}
	ts, err := s.scanTasks(ctx, rows)
	if err != nil {
		return Task{}, err
	}
	if len(ts) == 0 {
		return Task{}, ErrNotFound
	}
	return ts[0], nil
}

// TaskFilter selects tasks; empty fields match everything.
type TaskFilter struct {
	Statuses  []string
	ProjectID string
	ThreadID  string // origin thread
	ParentID  string
	Limit     int
}

// Tasks lists tasks by priority (higher first), then oldest first.
func (s *Store) Tasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	var args []any
	if len(f.Statuses) > 0 {
		q += ` AND status IN (?` + repeat(",?", len(f.Statuses)-1) + `)`
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.ThreadID != "" {
		q += ` AND origin_thread_id = ?`
		args = append(args, f.ThreadID)
	}
	if f.ParentID != "" {
		q += ` AND parent_id = ?`
		args = append(args, f.ParentID)
	}
	if f.Limit <= 0 {
		f.Limit = 500
	}
	q += ` ORDER BY priority DESC, created_at LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return s.scanTasks(ctx, rows)
}

type Project struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	RepoURL      string              `json:"repo_url"`
	DefaultMode  string              `json:"default_mode"`
	DefaultAgent string              `json:"default_agent"`
	Nodes        []event.ProjectNode `json:"nodes"`
	CreatedAt    int64               `json:"created_at"`
	UpdatedAt    int64               `json:"updated_at"`
}

// Projects lists projects by name, each with its machines in priority order.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, repo_url, default_mode, default_agent, created_at, updated_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	idx := map[string]int{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoURL, &p.DefaultMode, &p.DefaultAgent, &p.CreatedAt, &p.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		p.Nodes = []event.ProjectNode{}
		idx[p.ID] = len(out)
		out = append(out, p)
	}
	rows.Close()
	nodes, err := s.db.QueryContext(ctx, `SELECT project_id, node_id, path, priority FROM project_nodes ORDER BY priority, node_id`)
	if err != nil {
		return nil, err
	}
	defer nodes.Close()
	for nodes.Next() {
		var pid string
		var n event.ProjectNode
		if err := nodes.Scan(&pid, &n.NodeID, &n.Path, &n.Priority); err != nil {
			return nil, err
		}
		if i, ok := idx[pid]; ok {
			out[i].Nodes = append(out[i].Nodes, n)
		}
	}
	return out, nodes.Err()
}

func (s *Store) Project(ctx context.Context, id string) (Project, error) {
	ps, err := s.Projects(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

type PushSubscription struct {
	Endpoint  string `json:"endpoint"`
	P256DH    string `json:"p256dh"`
	Auth      string `json:"auth"`
	UserAgent string `json:"user_agent"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) PutPushSubscription(ctx context.Context, p PushSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO push_subscriptions(endpoint, p256dh, auth, user_agent, created_at) VALUES (?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, user_agent = excluded.user_agent`,
		p.Endpoint, p.P256DH, p.Auth, p.UserAgent, p.CreatedAt)
	return err
}

func (s *Store) DeletePushSubscription(ctx context.Context, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

func (s *Store) PushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT endpoint, p256dh, auth, user_agent, created_at FROM push_subscriptions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSubscription{}
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.Endpoint, &p.P256DH, &p.Auth, &p.UserAgent, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LastDispatch returns when the user or the supervisor last asked a task to
// run (a task.status to queued not made by the engine itself), or 0.
func (s *Store) LastDispatch(ctx context.Context, taskID string) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(ts) FROM events WHERE task_id = ? AND type = ? AND actor != ? AND json_extract(payload, '$.to') = ?`,
		taskID, string(event.TaskStatus), event.ActorSystem, event.TaskQueued).Scan(&ts)
	return ts.Int64, err
}

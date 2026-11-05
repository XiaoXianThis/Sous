package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

func projectRun(w *Writer, ev event.Event) error {
	exec := func(q string, args ...any) error {
		_, err := w.tx.ExecContext(w.ctx, q, args...)
		return err
	}
	switch ev.Type {
	case event.NodePaired:
		var p event.NodePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`INSERT INTO nodes(id, name, os, arch, hostname, created_at, last_seen) VALUES (?,?,?,?,?,?,?)`,
			p.NodeID, p.Name, p.OS, p.Arch, p.Hostname, ev.TS, ev.TS)
	case event.NodeUpdated:
		var p event.NodePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE nodes SET name = ? WHERE id = ?`, p.Name, p.NodeID)
	case event.NodeRevoked:
		var p event.NodePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if err := exec(`DELETE FROM node_tokens WHERE node_id = ?`, p.NodeID); err != nil {
			return err
		}
		return exec(`UPDATE nodes SET revoked = 1, online = 0 WHERE id = ?`, p.NodeID)
	case event.NodeOnline:
		var p event.NodePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE nodes SET online = 1, last_seen = ?, version = ?, os = ?, arch = ?, hostname = ? WHERE id = ?`,
			ev.TS, p.Version, p.OS, p.Arch, p.Hostname, p.NodeID)
	case event.NodeOffline:
		var p event.NodePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE nodes SET online = 0, last_seen = ? WHERE id = ?`, ev.TS, p.NodeID)
	case event.NodeInventory:
		var p event.NodePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		conc := p.Concurrency
		if conc <= 0 {
			conc = 2
		}
		if err := exec(`UPDATE nodes SET agents = ?, concurrency = ?, last_seen = ? WHERE id = ?`, string(p.Agents), conc, ev.TS, p.NodeID); err != nil {
			return err
		}
		if p.Role != "" {
			if err := exec(`UPDATE nodes SET role = ? WHERE id = ?`, p.Role, p.NodeID); err != nil {
				return err
			}
		}
		if p.Workspaces != nil {
			ws, _ := json.Marshal(p.Workspaces)
			if err := exec(`UPDATE nodes SET workspaces = ? WHERE id = ?`, string(ws), p.NodeID); err != nil {
				return err
			}
		}
		if p.Wok != nil {
			return exec(`UPDATE nodes SET wok = ? WHERE id = ?`, *p.Wok, p.NodeID)
		}
		return nil

	case event.RunQueued:
		var p event.RunQueuedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		purpose := p.Purpose
		if purpose == "" {
			purpose = event.PurposeWork
		}
		mode := p.PermissionMode
		if mode == "" {
			mode = "default"
		}
		opts, setup := "", ""
		if len(p.Options) > 0 {
			raw, _ := json.Marshal(p.Options)
			opts = string(raw)
		}
		if p.Setup != nil {
			raw, _ := json.Marshal(p.Setup)
			setup = string(raw)
		}
		return exec(`INSERT INTO runs(id, thread_id, task_id, node_id, agent, kind, repo_path, prompt, status, purpose, permission_mode, branch, base_branch, title, force, recovery_note, created_at, last_event, options, persona, setup)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			ev.RunID, ev.ThreadID, ev.TaskID, p.NodeID, p.Agent, p.Kind, p.RepoPath, p.Prompt, event.RunStatusQueued, purpose, mode, p.Branch, p.BaseBranch, p.Title, p.Force, p.RecoveryNote, ev.TS, ev.TS, opts, p.Persona, setup)
	case event.RunStarted:
		var p event.RunStartedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if err := exec(`UPDATE runs SET status = ?, workdir = ?, branch = ?, base_branch = CASE WHEN ? = '' THEN base_branch ELSE ? END,
			started_at = CASE WHEN started_at = 0 THEN ? ELSE started_at END, last_event = ? WHERE id = ?`,
			event.RunStatusRunning, p.Workdir, p.Branch, p.BaseBranch, p.BaseBranch, ev.TS, ev.TS, ev.RunID); err != nil {
			return err
		}
		// The first run of a code task names the task's branch.
		return exec(`UPDATE tasks SET branch = CASE WHEN branch = '' THEN ? ELSE branch END,
			base_branch = CASE WHEN base_branch = '' THEN ? ELSE base_branch END
			WHERE id = (SELECT task_id FROM runs WHERE id = ?) AND ? != ''`, p.Branch, p.BaseBranch, ev.RunID, p.Branch)
	case event.RunUpdate:
		// The event log is the record; the run row keeps only its state and
		// when it last showed progress (for the inspector).
		return exec(`UPDATE runs SET last_event = ? WHERE id = ?`, ev.TS, ev.RunID)
	case event.RunPaused:
		var p event.RunPausePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if err := exec(`UPDATE runs SET paused = 1, pause_reason = ? WHERE id = ? AND status IN ('queued', 'running')`, p.Reason, ev.RunID); err != nil {
			return err
		}
		// Pausing cancels the agent's turn and with it what it was asking.
		return exec(`UPDATE approvals SET status = 'expired', decided_at = ? WHERE run_id = ? AND status = 'pending'`, ev.TS, ev.RunID)
	case event.RunResumed:
		return exec(`UPDATE runs SET paused = 0, pause_reason = '', last_event = ? WHERE id = ?`, ev.TS, ev.RunID)
	case event.RunMessage:
		var p event.RunMessagePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`INSERT OR IGNORE INTO run_messages(id, run_id, text, created_at) VALUES (?,?,?,?)`, p.MessageID, ev.RunID, p.Text, ev.TS)
	case event.RunMessageDelivered:
		var p event.RunDeliveredPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		for _, id := range p.MessageIDs {
			if err := exec(`UPDATE run_messages SET delivered = 1 WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	case event.RunArchived:
		return exec(`UPDATE runs SET archived = 1 WHERE id = ?`, ev.RunID)
	case event.RunEnded:
		var p event.RunEndedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		if err := exec(`UPDATE runs SET status = ?, summary = ?, diffstat = ?, error = ?, pr_url = ?, conflict = ?, ended_at = ?, paused = 0 WHERE id = ?`,
			p.Status, p.Summary, p.DiffStat, p.Error, p.PRURL, p.Conflict, ev.TS, ev.RunID); err != nil {
			return err
		}
		// Questions the run left open can no longer be answered.
		return exec(`UPDATE approvals SET status = 'expired', decided_at = ? WHERE run_id = ? AND status = 'pending'`, ev.TS, ev.RunID)

	case event.ApprovalRequested:
		var p event.ApprovalRequestedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		opts, _ := json.Marshal(p.Options)
		review := p.Review
		if review == "" {
			review = "user"
		}
		return exec(`INSERT INTO approvals(id, run_id, thread_id, task_id, title, tool_kind, detail, options, category, review, status, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ApprovalID, ev.RunID, ev.ThreadID, ev.TaskID, p.Title, p.ToolKind, p.Detail, string(opts), p.Category, review, "pending", ev.TS)
	case event.ApprovalEscalated:
		var p event.ApprovalEscalatedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE approvals SET review = 'user', escalation = ? WHERE id = ? AND status = 'pending'`, p.Note, p.ApprovalID)
	case event.ApprovalDecided:
		var p event.ApprovalDecidedPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE approvals SET status = 'decided', option_id = ?, decided_by = ?, note = ?, decided_at = ? WHERE id = ? AND status = 'pending'`,
			p.OptionID, p.DecidedBy, p.Note, ev.TS, p.ApprovalID)
	}
	return nil
}

type Node struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	OS          string          `json:"os"`
	Arch        string          `json:"arch"`
	Hostname    string          `json:"hostname"`
	Version     string          `json:"version"`
	Agents      json.RawMessage `json:"agents"`
	Online      bool            `json:"online"`
	Concurrency int             `json:"concurrency"`
	LastSeen    int64           `json:"last_seen"`
	CreatedAt   int64           `json:"created_at"`
	Revoked     bool            `json:"revoked"`
	// M5
	Role       string   `json:"role"`
	Workspaces []string `json:"workspaces"`
	Wok        string   `json:"wok"`
	// Admin is live state, not stored: "on" when the connected node
	// accepts remote administration, "off" when its node.json turned it
	// off, "unsupported" for an older daemon, empty when offline.
	Admin string `json:"admin,omitempty"`
}

const nodeCols = `id, name, os, arch, hostname, version, agents, online, concurrency, last_seen, created_at, revoked, role, workspaces, wok`

func scanNodes(rows *sql.Rows) ([]Node, error) {
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var n Node
		var agents, ws string
		if err := rows.Scan(&n.ID, &n.Name, &n.OS, &n.Arch, &n.Hostname, &n.Version, &agents, &n.Online, &n.Concurrency, &n.LastSeen, &n.CreatedAt, &n.Revoked,
			&n.Role, &ws, &n.Wok); err != nil {
			return nil, err
		}
		n.Agents = json.RawMessage(agents)
		json.Unmarshal([]byte(ws), &n.Workspaces)
		n.Workspaces = nonNilSlice(n.Workspaces)
		out = append(out, n)
	}
	return out, rows.Err()
}

// Nodes lists the machines that are not revoked.
func (s *Store) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE revoked = 0 ORDER BY online DESC, name`)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

func (s *Store) Node(ctx context.Context, id string) (Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id)
	if err != nil {
		return Node{}, err
	}
	ns, err := scanNodes(rows)
	if err != nil {
		return Node{}, err
	}
	if len(ns) == 0 {
		return Node{}, ErrNotFound
	}
	return ns[0], nil
}

// SetNodeToken stores the hash of a node's device token.
func (s *Store) SetNodeToken(ctx context.Context, nodeID, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO node_tokens(node_id, token_hash) VALUES (?,?) ON CONFLICT(node_id) DO UPDATE SET token_hash = excluded.token_hash`,
		nodeID, tokenHash)
	return err
}

// NodeByToken returns the node a device token belongs to, or ErrNotFound.
func (s *Store) NodeByToken(ctx context.Context, tokenHash string) (Node, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT t.node_id FROM node_tokens t JOIN nodes n ON n.id = t.node_id WHERE t.token_hash = ? AND n.revoked = 0`, tokenHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, err
	}
	return s.Node(ctx, id)
}

type Run struct {
	ID         string `json:"id"`
	ThreadID   string `json:"thread_id"`
	TaskID     string `json:"task_id"`
	Purpose    string `json:"purpose"`
	Mode       string `json:"permission_mode"`
	BaseBranch string `json:"base_branch"`
	PRURL      string `json:"pr_url"`
	Conflict   bool   `json:"conflict"`
	Title      string `json:"title"`
	Force      bool   `json:"force"`
	NodeID     string `json:"node_id"`
	Agent      string `json:"agent"`
	Kind       string `json:"kind"`
	RepoPath   string `json:"repo_path"`
	Prompt     string `json:"prompt"`
	Status     string `json:"status"`
	Workdir    string `json:"workdir"`
	Branch     string `json:"branch"`
	Summary    string `json:"summary"`
	DiffStat   string `json:"diffstat"`
	Error      string `json:"error"`
	CreatedAt  int64  `json:"created_at"`
	StartedAt  int64  `json:"started_at"`
	EndedAt    int64  `json:"ended_at"`
	// M4
	Paused       bool   `json:"paused"`
	PauseReason  string `json:"pause_reason"`
	Archived     bool   `json:"archived"`
	RecoveryNote string `json:"recovery_note"`
	LastEvent    int64  `json:"last_event"`
	// M5
	Options map[string]string `json:"options,omitempty"`
	Persona string            `json:"persona,omitempty"`
	Setup   *proto.Setup      `json:"setup,omitempty"`
}

const runCols = `id, thread_id, task_id, purpose, permission_mode, base_branch, pr_url, conflict, title, force, node_id, agent, kind, repo_path, prompt, status, workdir, branch, summary, diffstat, error, created_at, started_at, ended_at,
	paused, pause_reason, archived, recovery_note, last_event, options, persona, setup`

func scanRuns(rows *sql.Rows) ([]Run, error) {
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var opts, setup string
		if err := rows.Scan(&r.ID, &r.ThreadID, &r.TaskID, &r.Purpose, &r.Mode, &r.BaseBranch, &r.PRURL, &r.Conflict, &r.Title, &r.Force, &r.NodeID, &r.Agent, &r.Kind, &r.RepoPath, &r.Prompt, &r.Status,
			&r.Workdir, &r.Branch, &r.Summary, &r.DiffStat, &r.Error, &r.CreatedAt, &r.StartedAt, &r.EndedAt,
			&r.Paused, &r.PauseReason, &r.Archived, &r.RecoveryNote, &r.LastEvent, &opts, &r.Persona, &setup); err != nil {
			return nil, err
		}
		if opts != "" {
			json.Unmarshal([]byte(opts), &r.Options)
		}
		if setup != "" {
			var st proto.Setup
			if json.Unmarshal([]byte(setup), &st) == nil {
				r.Setup = &st
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Run(ctx context.Context, id string) (Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id)
	if err != nil {
		return Run{}, err
	}
	rs, err := scanRuns(rows)
	if err != nil {
		return Run{}, err
	}
	if len(rs) == 0 {
		return Run{}, ErrNotFound
	}
	return rs[0], nil
}

// RunFilter selects runs; empty fields match everything.
type RunFilter struct {
	ThreadID string
	TaskID   string
	NodeID   string
	Statuses []string
	Limit    int
}

// Runs lists runs, oldest first.
func (s *Store) Runs(ctx context.Context, f RunFilter) ([]Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE 1=1`
	var args []any
	if f.ThreadID != "" {
		q += ` AND thread_id = ?`
		args = append(args, f.ThreadID)
	}
	if f.TaskID != "" {
		q += ` AND task_id = ?`
		args = append(args, f.TaskID)
	}
	if f.NodeID != "" {
		q += ` AND node_id = ?`
		args = append(args, f.NodeID)
	}
	if len(f.Statuses) > 0 {
		q += ` AND status IN (?` + repeat(",?", len(f.Statuses)-1) + `)`
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.Limit <= 0 {
		f.Limit = 200
	}
	q += ` ORDER BY created_at LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanRuns(rows)
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}

// RunEvents returns the stored events of a run, oldest first.
func (s *Store) RunEvents(ctx context.Context, runID string) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, id, ts, type, actor, IFNULL(thread_id,''), IFNULL(task_id,''), IFNULL(run_id,''), payload
		 FROM events WHERE run_id = ? ORDER BY seq`, runID)
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

type Approval struct {
	ID         string                 `json:"id"`
	RunID      string                 `json:"run_id"`
	TaskID     string                 `json:"task_id"`
	Category   string                 `json:"category"`
	Review     string                 `json:"review"` // user / ai
	Escalation string                 `json:"escalation"`
	Note       string                 `json:"note"`
	ThreadID   string                 `json:"thread_id"`
	Title      string                 `json:"title"`
	ToolKind   string                 `json:"tool_kind"`
	Detail     string                 `json:"detail"`
	Options    []event.ApprovalOption `json:"options"`
	Status     string                 `json:"status"` // pending / decided / expired
	OptionID   string                 `json:"option_id"`
	DecidedBy  string                 `json:"decided_by"`
	CreatedAt  int64                  `json:"created_at"`
	DecidedAt  int64                  `json:"decided_at"`
}

const approvalCols = `id, run_id, task_id, category, review, escalation, note, thread_id, title, tool_kind, detail, options, status, option_id, decided_by, created_at, decided_at`

func scanApprovals(rows *sql.Rows) ([]Approval, error) {
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var a Approval
		var opts string
		if err := rows.Scan(&a.ID, &a.RunID, &a.TaskID, &a.Category, &a.Review, &a.Escalation, &a.Note, &a.ThreadID, &a.Title, &a.ToolKind, &a.Detail, &opts, &a.Status,
			&a.OptionID, &a.DecidedBy, &a.CreatedAt, &a.DecidedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(opts), &a.Options)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Approval(ctx context.Context, id string) (Approval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+approvalCols+` FROM approvals WHERE id = ?`, id)
	if err != nil {
		return Approval{}, err
	}
	as, err := scanApprovals(rows)
	if err != nil {
		return Approval{}, err
	}
	if len(as) == 0 {
		return Approval{}, ErrNotFound
	}
	return as[0], nil
}

// Approvals lists approvals with the given status (empty: all), oldest first.
func (s *Store) Approvals(ctx context.Context, status, runID string) ([]Approval, error) {
	q := `SELECT ` + approvalCols + ` FROM approvals WHERE 1=1`
	var args []any
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if runID != "" {
		q += ` AND run_id = ?`
		args = append(args, runID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	return scanApprovals(rows)
}

// TaskApprovals lists a task's approvals across all its runs, oldest first.
func (s *Store) TaskApprovals(ctx context.Context, taskID string) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+approvalCols+` FROM approvals WHERE task_id = ? ORDER BY created_at LIMIT 500`, taskID)
	if err != nil {
		return nil, err
	}
	return scanApprovals(rows)
}

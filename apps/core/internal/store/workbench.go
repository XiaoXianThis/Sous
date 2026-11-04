package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	projectkey "github.com/XiaoXianThis/Sous/apps/core/internal/project"
)

// Milestone M5: personas, repositories found in workspaces, machine roles.

func projectWorkbench(w *Writer, ev event.Event) error {
	exec := func(q string, args ...any) error {
		_, err := w.tx.ExecContext(w.ctx, q, args...)
		return err
	}
	switch ev.Type {
	case event.PersonaPut:
		var p event.PersonaPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		opts, _ := json.Marshal(nonNilMap(p.Options))
		kinds, _ := json.Marshal(nonNilSlice(p.Kinds))
		return exec(`INSERT INTO personas(id, name, glyph, color, agent, options, brief, default_mode, kinds, builtin, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, glyph = excluded.glyph, color = excluded.color, agent = excluded.agent,
			  options = excluded.options, brief = excluded.brief, default_mode = excluded.default_mode, kinds = excluded.kinds,
			  updated_at = excluded.updated_at`,
			p.PersonaID, p.Name, p.Glyph, p.Color, p.Agent, string(opts), p.Brief, p.DefaultMode, string(kinds), p.Builtin, ev.TS, ev.TS)

	case event.PersonaDeleted:
		var p event.PersonaPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`DELETE FROM personas WHERE id = ?`, p.PersonaID)

	case event.NodeRepos:
		var p event.NodeReposPayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		ws, _ := json.Marshal(nonNilSlice(p.Workspaces))
		if err := exec(`UPDATE nodes SET workspaces = ? WHERE id = ?`, string(ws), p.NodeID); err != nil {
			return err
		}
		if err := exec(`DELETE FROM repo_scans WHERE node_id = ?`, p.NodeID); err != nil {
			return err
		}
		for _, r := range p.Repos {
			if err := exec(`INSERT OR REPLACE INTO repo_scans(node_id, path, remote, remote_key, branch, dirty, wokspace, seen_at) VALUES (?,?,?,?,?,?,?,?)`,
				p.NodeID, r.Path, r.Remote, projectkey.Key(r.Remote), r.Branch, r.Dirty, r.Wokspace, ev.TS); err != nil {
				return err
			}
		}
		return linkScans(w)

	case event.NodeRole:
		var p event.NodeRolePayload
		if err := ev.Decode(&p); err != nil {
			return err
		}
		return exec(`UPDATE nodes SET role = ? WHERE id = ?`, p.Role, p.NodeID)
	}
	return nil
}

// linkScans keeps the "scan" places of projects in step with the
// repositories the machines reported: a repository whose remote is a
// project's becomes one of its places; a place whose repository is gone is
// removed. Places entered by hand are never touched.
func linkScans(w *Writer) error {
	exec := func(q string, args ...any) error {
		_, err := w.tx.ExecContext(w.ctx, q, args...)
		return err
	}
	if err := exec(`DELETE FROM project_nodes WHERE source = 'scan' AND NOT EXISTS (
		SELECT 1 FROM repo_scans s JOIN projects p ON p.id = project_nodes.project_id
		WHERE s.node_id = project_nodes.node_id AND s.path = project_nodes.path AND p.remote_key != '' AND s.remote_key = p.remote_key)`); err != nil {
		return err
	}
	if err := exec(`INSERT OR IGNORE INTO project_nodes(project_id, node_id, path, priority, source)
		SELECT p.id, s.node_id, MIN(s.path), 100, 'scan' FROM repo_scans s JOIN projects p ON p.remote_key != '' AND s.remote_key = p.remote_key
		GROUP BY p.id, s.node_id`); err != nil {
		return err
	}
	return exec(`UPDATE projects SET layout = 'wokspace' WHERE layout = '' AND id IN (
		SELECT pn.project_id FROM project_nodes pn JOIN repo_scans s ON s.node_id = pn.node_id AND s.path = pn.path WHERE s.wokspace = 1)`)
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type Persona struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Glyph       string            `json:"glyph"`
	Color       string            `json:"color"`
	Agent       string            `json:"agent"`
	Options     map[string]string `json:"options"`
	Brief       string            `json:"brief"`
	DefaultMode string            `json:"default_mode"`
	Kinds       []string          `json:"kinds"`
	Builtin     bool              `json:"builtin"`
	CreatedAt   int64             `json:"created_at"`
	UpdatedAt   int64             `json:"updated_at"`
}

// Personas lists personas, built-in ones first, then by name.
func (s *Store) Personas(ctx context.Context) ([]Persona, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, glyph, color, agent, options, brief, default_mode, kinds, builtin, created_at, updated_at
		FROM personas ORDER BY builtin DESC, created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Persona{}
	for rows.Next() {
		var p Persona
		var opts, kinds string
		if err := rows.Scan(&p.ID, &p.Name, &p.Glyph, &p.Color, &p.Agent, &opts, &p.Brief, &p.DefaultMode, &kinds, &p.Builtin, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(opts), &p.Options)
		json.Unmarshal([]byte(kinds), &p.Kinds)
		p.Options = nonNilMap(p.Options)
		p.Kinds = nonNilSlice(p.Kinds)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Persona(ctx context.Context, id string) (Persona, error) {
	ps, err := s.Personas(ctx)
	if err != nil {
		return Persona{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return Persona{}, ErrNotFound
}

// RepoScan is a repository a machine found in its workspaces.
type RepoScan struct {
	NodeID    string `json:"node_id"`
	Path      string `json:"path"`
	Remote    string `json:"remote"`
	RemoteKey string `json:"remote_key"`
	Branch    string `json:"branch"`
	Dirty     bool   `json:"dirty"`
	Wokspace  bool   `json:"wokspace"`
	SeenAt    int64  `json:"seen_at"`
	// ProjectID is the project it belongs to, if any.
	ProjectID string `json:"project_id"`
}

// RepoScans lists the repositories the machines found, each with the
// project it is a place of (by remote, or by a place entered by hand).
func (s *Store) RepoScans(ctx context.Context) ([]RepoScan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.node_id, s.path, s.remote, s.remote_key, s.branch, s.dirty, s.wokspace, s.seen_at,
		COALESCE((SELECT pn.project_id FROM project_nodes pn WHERE pn.node_id = s.node_id AND pn.path = s.path LIMIT 1),
		         (SELECT p.id FROM projects p WHERE p.remote_key != '' AND p.remote_key = s.remote_key LIMIT 1), '')
		FROM repo_scans s JOIN nodes n ON n.id = s.node_id AND n.revoked = 0 ORDER BY s.node_id, s.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RepoScan{}
	for rows.Next() {
		var r RepoScan
		if err := rows.Scan(&r.NodeID, &r.Path, &r.Remote, &r.RemoteKey, &r.Branch, &r.Dirty, &r.Wokspace, &r.SeenAt, &r.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProjectByKey finds the project with a remote key.
func (s *Store) ProjectByKey(ctx context.Context, key string) (Project, error) {
	if key == "" {
		return Project{}, ErrNotFound
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE remote_key = ?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	} else if err != nil {
		return Project{}, err
	}
	return s.Project(ctx, id)
}

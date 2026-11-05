package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// MCPServer is one MCP server the supervisor can use (internal/mcp). Like
// model providers it is local configuration, not a domain event, so it
// bypasses the event log. Env and Headers hold secrets: the API only ever
// returns their names.
type MCPServer struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // stdio | http
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	URL       string            `json:"url"`
	Env       map[string]string `json:"-"`
	Headers   map[string]string `json:"-"`
	// DisabledTools are tool names (as the server calls them) hidden from the model.
	DisabledTools []string `json:"disabled_tools"`
	Enabled       bool     `json:"enabled"`
	// Pending: the agent proposed this server and the user has not approved
	// it yet. A pending server never starts.
	Pending bool `json:"pending"`
	// Source is who added it: user or agent.
	Source string `json:"source"`
	// Note is why the agent wants it.
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

const mcpCols = `id, name, transport, command, args, url, env, headers, disabled_tools, enabled, pending, source, note, created_at, updated_at`

func (s *Store) MCPServers(ctx context.Context) ([]MCPServer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mcpCols+` FROM mcp_servers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MCPServer{}
	for rows.Next() {
		var m MCPServer
		var args, env, headers, disabled string
		var enabled, pending int
		if err := rows.Scan(&m.ID, &m.Name, &m.Transport, &m.Command, &args, &m.URL, &env, &headers, &disabled,
			&enabled, &pending, &m.Source, &m.Note, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(args), &m.Args)
		json.Unmarshal([]byte(env), &m.Env)
		json.Unmarshal([]byte(headers), &m.Headers)
		json.Unmarshal([]byte(disabled), &m.DisabledTools)
		if m.Args == nil {
			m.Args = []string{}
		}
		if m.Env == nil {
			m.Env = map[string]string{}
		}
		if m.Headers == nil {
			m.Headers = map[string]string{}
		}
		if m.DisabledTools == nil {
			m.DisabledTools = []string{}
		}
		m.Enabled, m.Pending = enabled != 0, pending != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MCPServer(ctx context.Context, id string) (MCPServer, error) {
	all, err := s.MCPServers(ctx)
	if err != nil {
		return MCPServer{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return MCPServer{}, ErrNotFound
}

// PutMCPServer inserts or replaces a server.
func (s *Store) PutMCPServer(ctx context.Context, m MCPServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_servers(`+mcpCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, transport = excluded.transport, command = excluded.command,
		args = excluded.args, url = excluded.url, env = excluded.env, headers = excluded.headers,
		disabled_tools = excluded.disabled_tools, enabled = excluded.enabled, pending = excluded.pending,
		note = excluded.note, updated_at = excluded.updated_at`,
		m.ID, m.Name, m.Transport, m.Command, enc(orEmpty(m.Args)), m.URL, enc(orEmptyMap(m.Env)), enc(orEmptyMap(m.Headers)),
		enc(orEmpty(m.DisabledTools)), b2i(m.Enabled), b2i(m.Pending), m.Source, m.Note, now, now)
	return err
}

func (s *Store) DeleteMCPServer(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

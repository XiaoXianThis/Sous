package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Fresh is the Hub's recent-login check (answers 401 "reauth" itself).
// Anything that changes what runs on the Hub, or where a token is sent,
// needs it: a stolen cookie must not be enough to run a program.
type Fresh func(http.ResponseWriter, *http.Request) bool

// Routes serves /api/v1/mcp.
func (m *Manager) Routes(fresh Fresh) func(*http.ServeMux, func(string, http.HandlerFunc)) {
	return func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		protect("GET /api/v1/mcp", m.get)
		protect("POST /api/v1/mcp/servers", m.guard(fresh, m.create))
		protect("PATCH /api/v1/mcp/servers/{id}", m.patch(fresh))
		protect("POST /api/v1/mcp/servers/{id}/approve", m.guard(fresh, m.approve))
		protect("POST /api/v1/mcp/servers/{id}/reconnect", m.reconnect)
		protect("DELETE /api/v1/mcp/servers/{id}", m.remove)
	}
}

func (m *Manager) guard(fresh Fresh, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if fresh != nil && !fresh(w, req) {
			return
		}
		h(w, req)
	}
}

func (m *Manager) get(w http.ResponseWriter, req *http.Request) {
	vs, err := m.Views(req.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": vs, "stdio_allowed": !m.StdioDisabled})
}

// body is the JSON the page sends to create or change a server.
type body struct {
	Name          *string           `json:"name"`
	Transport     *string           `json:"transport"`
	Command       *string           `json:"command"`
	Args          *[]string         `json:"args"`
	URL           *string           `json:"url"`
	Env           map[string]string `json:"env"`
	RemoveEnv     []string          `json:"remove_env"`
	Headers       map[string]string `json:"headers"`
	RemoveHeaders []string          `json:"remove_headers"`
	Enabled       *bool             `json:"enabled"`
	DisabledTools *[]string         `json:"disabled_tools"`
}

func (b body) patch() Patch {
	return Patch{Name: b.Name, Transport: b.Transport, Command: b.Command, Args: b.Args, URL: b.URL,
		Env: b.Env, RemoveEnv: b.RemoveEnv, Headers: b.Headers, RemoveHeaders: b.RemoveHeaders,
		Enabled: b.Enabled, DisabledTools: b.DisabledTools}
}

func decode(w http.ResponseWriter, req *http.Request) (body, bool) {
	var b body
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return b, false
	}
	return b, true
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (m *Manager) create(w http.ResponseWriter, req *http.Request) {
	b, ok := decode(w, req)
	if !ok {
		return
	}
	cfg := store.MCPServer{
		Name: str(b.Name), Transport: str(b.Transport), Command: str(b.Command), URL: str(b.URL),
		Env: b.Env, Headers: b.Headers, Enabled: true, Source: "user",
	}
	if b.Args != nil {
		cfg.Args = *b.Args
	}
	if b.Enabled != nil {
		cfg.Enabled = *b.Enabled
	}
	if _, err := m.Add(req.Context(), cfg); err != nil {
		fail(w, err)
		return
	}
	m.get(w, req)
}

// patch changes a server. Changes to what runs or where secrets go need a
// recent login; switching a server or one of its tools on or off does not.
func (m *Manager) patch(fresh Fresh) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, ok := decode(w, req)
		if !ok {
			return
		}
		p := b.patch()
		if p.Sensitive() && fresh != nil && !fresh(w, req) {
			return
		}
		if _, err := m.Update(req.Context(), req.PathValue("id"), p); err != nil {
			fail(w, err)
			return
		}
		m.get(w, req)
	}
}

func (m *Manager) approve(w http.ResponseWriter, req *http.Request) {
	if _, err := m.Approve(req.Context(), req.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	m.get(w, req)
}

func (m *Manager) reconnect(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), connectTimeout+5*time.Second)
	defer cancel()
	if _, err := m.Reconnect(ctx, req.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	m.get(w, req)
}

func (m *Manager) remove(w http.ResponseWriter, req *http.Request) {
	if err := m.Remove(req.Context(), req.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	m.get(w, req)
}

func fail(w http.ResponseWriter, err error) {
	var inv ErrInvalid
	var con ErrConflict
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusBadRequest, "invalid", inv.Msg)
	case errors.As(err, &con):
		writeError(w, http.StatusConflict, "conflict", con.Msg)
	case errors.Is(err, store.ErrNotFound), isNoRows(err):
		writeError(w, http.StatusNotFound, "not_found", "MCP 服务不存在")
	default:
		internal(w, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func internal(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Error("mcp: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

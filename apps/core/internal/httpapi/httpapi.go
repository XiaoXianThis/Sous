// Package httpapi serves the Hub's client API (docs/api/core-hub-http.md):
// REST under /api/v1, the event stream at /ws, and the built PWA.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

const maxMessageRunes = 20000

type Server struct {
	cfg  *config.Hub
	log  *eventlog.Log
	auth *auth.Auth
	mux  *http.ServeMux
}

// Routes lets other packages add endpoints. protect wraps a handler so it
// requires a logged-in user and, for writes, the CSRF token.
type Routes func(mux *http.ServeMux, protect func(pattern string, h http.HandlerFunc))

func New(cfg *config.Hub, log *eventlog.Log, a *auth.Auth, extra ...Routes) *Server {
	s := &Server{cfg: cfg, log: log, auth: a, mux: http.NewServeMux()}
	a.Routes(s.mux)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
	})
	protect := func(pattern string, h http.HandlerFunc) { s.mux.Handle(pattern, a.Require(h)) }
	protect("GET /api/v1/threads", s.listThreads)
	protect("POST /api/v1/threads", s.createThread)
	protect("GET /api/v1/threads/{id}", s.getThread)
	protect("GET /api/v1/threads/{id}/messages", s.listMessages)
	protect("POST /api/v1/threads/{id}/messages", s.postMessage)
	protect("GET /api/v1/usage", s.usage)
	s.memoryRoutes(protect)
	for _, r := range extra {
		r(s.mux, protect)
	}
	protect("GET /ws", s.ws)
	s.mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	}))
	s.mux.Handle("/", s.static())
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: 200}
	s.mux.ServeHTTP(sw, r)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
	}
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	ts, err := s.log.Store.Threads(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": ts})
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "新对话"
	}
	ev := event.New(event.ThreadCreated, event.ActorUser, "", event.ThreadCreatedPayload{Title: title, Kind: "chat"})
	ev.ThreadID = ev.ID
	if _, err := s.log.Append(r.Context(), ev); err != nil {
		internal(w, err)
		return
	}
	t, err := s.log.Store.Thread(r.Context(), ev.ThreadID)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	t, err := s.log.Store.Thread(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "对话不存在")
		return
	} else if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.log.Store.Thread(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "对话不存在")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	msgs, err := s.log.Store.Messages(r.Context(), id, before, limit)
	if err != nil {
		internal(w, err)
		return
	}
	last, _ := s.log.Store.LastSeq(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "last_seq": last})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.log.Store.Thread(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "对话不存在")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "empty", "消息不能为空")
		return
	}
	if utf8.RuneCountInString(content) > maxMessageRunes {
		writeError(w, http.StatusBadRequest, "too_long", "消息太长")
		return
	}
	msgID := ids.New()
	evs, err := s.log.Append(r.Context(), event.New(event.MessageUser, event.ActorUser, id,
		event.MessagePayload{MessageID: msgID, Content: content}))
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"message_id": msgID, "seq": evs[0].Seq})
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 30
	}
	rows, err := s.log.Store.UsageSince(r.Context(), time.Now().AddDate(0, 0, -days))
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "usage": rows})
}

// static serves the built PWA with a fallback to index.html for client routes.
func (s *Server) static() http.Handler {
	dir := s.cfg.WebDir
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dir == "" {
			http.Error(w, "web_dir is not configured", http.StatusNotFound)
			return
		}
		index := filepath.Join(dir, "index.html")
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "PWA not built: run `wok build web` first", http.StatusNotFound)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		p := filepath.Join(dir, filepath.FromSlash(clean))
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			// Client routes get the app shell; a missing file (such as an
			// asset from an older build) is a 404, never the shell.
			if path.Ext(clean) != "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, index)
			return
		}
		switch {
		case clean == "/sw.js" || strings.HasSuffix(clean, ".webmanifest"):
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(clean, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFile(w, r, p)
	})
}

// Shutdown hook for tests and main.
func (s *Server) Handler() http.Handler { return s }

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
	slog.Error("http: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController and websocket reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

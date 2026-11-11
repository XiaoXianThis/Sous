// Package httpapi serves the Hub's client API (docs/api/core-hub-http.md):
// REST under /api/v1, the event stream at /ws, and the built PWA.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/attach"
	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

const (
	maxMessageRunes = 20000
	// maxBodyBytes caps every request body; the largest legitimate one is a
	// file edited on the page (1 MB of content, JSON-escaped).
	maxBodyBytes = 4 << 20
	// maxUploadBytes caps an attachment upload (attach.MaxSize plus slack).
	maxUploadBytes = attach.MaxSize + 1<<20
)

type Server struct {
	cfg  *config.Hub
	log  *eventlog.Log
	auth *auth.Auth
	mux  *http.ServeMux
	// files holds the attachments of messages.
	files *attach.Store
}

// Routes lets other packages add endpoints. protect wraps a handler so it
// requires a logged-in user and, for writes, the CSRF token.
type Routes func(mux *http.ServeMux, protect func(pattern string, h http.HandlerFunc))

func New(cfg *config.Hub, log *eventlog.Log, a *auth.Auth, extra ...Routes) *Server {
	s := &Server{cfg: cfg, log: log, auth: a, mux: http.NewServeMux(), files: attach.Open(filepath.Join(cfg.DataDir, "attachments"))}
	sysmap.RegisterProbe("httpapi", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		p, d := pageConns.Load(), deviceConns.Load()
		return sysmap.Status{State: sysmap.OK, Summary: fmt.Sprintf("%d 个页面、%d 台设备连着", p, d), Gauges: []sysmap.Gauge{{Label: "连接", Value: float64(p + d)}}}
	}))
	sysmap.RegisterProbe("ui", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		n := pageConns.Load()
		if n == 0 {
			return sysmap.Status{State: sysmap.Idle, Summary: "没有打开的页面"}
		}
		return sysmap.Status{State: sysmap.OK, Summary: fmt.Sprintf("%d 个页面打开着", n)}
	}))
	sysmap.RegisterProbe("cli", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		n := deviceConns.Load()
		if n == 0 {
			return sysmap.Status{State: sysmap.Idle, Summary: "没有命令行连着"}
		}
		return sysmap.Status{State: sysmap.OK, Summary: fmt.Sprintf("%d 台设备连着", n)}
	}))
	a.Routes(s.mux)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// Both builds, so a stale page (an old build/web) shows (review 4.23).
		out := map[string]any{"status": "ok", "version": version.Version, "commit": version.BuildCommit()}
		if web, ok := version.ReadWeb(cfg.WebDir); ok {
			out["web"] = web
		}
		writeJSON(w, http.StatusOK, out)
	})
	protect := func(pattern string, h http.HandlerFunc) { s.mux.Handle(pattern, a.Require(h)) }
	protect("GET /api/v1/threads", s.listThreads)
	protect("POST /api/v1/threads", s.createThread)
	protect("GET /api/v1/threads/{id}", s.getThread)
	protect("GET /api/v1/threads/{id}/messages", s.listMessages)
	protect("POST /api/v1/threads/{id}/messages", s.postMessage)
	protect("GET /api/v1/threads/{id}/topics", s.listTopics)
	protect("GET /api/v1/topics/{id}", s.getTopic)
	protect("PATCH /api/v1/topics/{id}", s.patchTopic)
	protect("POST /api/v1/messages/{id}/move", s.moveMessage)
	protect("POST /api/v1/attachments", s.uploadAttachment)
	protect("GET /api/v1/attachments/{id}", s.getAttachment)
	protect("GET /api/v1/usage", s.usage)
	s.memoryRoutes(protect)
	s.noteRoutes(protect)
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
	s.secureHeaders(w.Header())
	if r.Body != nil {
		limit := int64(maxBodyBytes)
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/attachments" {
			limit = maxUploadBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	// Counted before the handler runs, so the request comes before what it
	// causes on the system page.
	if strings.HasPrefix(r.URL.Path, "/api/v1/device/") {
		sysmap.Count("cli>httpapi", refsOfPath(r.URL.Path), r.Method+" "+r.URL.Path)
	} else if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/v1/system/") {
		sysmap.Count("ui>httpapi", refsOfPath(r.URL.Path), r.Method+" "+r.URL.Path)
	}
	sw := &statusWriter{ResponseWriter: w, status: 200}
	s.mux.ServeHTTP(sw, r)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start))
	}
}

// refsOfPath finds the conversation, task or run a request acts on, so the
// system page groups it with what it causes.
func refsOfPath(p string) sysmap.Refs {
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(p, "/api/v1/"), "device/"), "/")
	if len(parts) < 2 || parts[1] == "" {
		return sysmap.Refs{}
	}
	switch parts[0] {
	case "threads":
		return sysmap.Refs{ThreadID: parts[1]}
	case "tasks":
		return sysmap.Refs{TaskID: parts[1]}
	case "runs":
		return sysmap.Refs{RunID: parts[1]}
	}
	return sysmap.Refs{}
}

// secureHeaders is set on every answer. The PWA loads nothing from other
// origins, runs no inline script and is never framed (approvals and the
// terminal must not be clickjacked). HSTS once the Hub is served over TLS.
func (s *Server) secureHeaders(h http.Header) {
	connect := "'self'"
	if u, err := url.Parse(s.cfg.PublicURL); err == nil && u.Host != "" {
		ws := "wss://"
		if u.Scheme == "http" {
			ws = "ws://"
		}
		connect += " " + ws + u.Host // older Safari does not count ws: as 'self'
	}
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data: blob:; font-src 'self' data:; connect-src "+connect+"; worker-src 'self'; manifest-src 'self'; "+
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if s.cfg.Secure() {
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	}
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	var ts []store.Thread
	var err error
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		ts, err = s.log.Store.ProjectThreads(r.Context(), pid)
	} else {
		ts, err = s.log.Store.Threads(r.Context())
	}
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": ts})
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string `json:"title"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "新对话"
	}
	payload := event.ThreadCreatedPayload{Title: title, Kind: "chat"}
	if body.ProjectID != "" {
		if _, err := s.log.Store.Project(r.Context(), body.ProjectID); errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "项目不存在")
			return
		} else if err != nil {
			internal(w, err)
			return
		}
		payload.Kind, payload.ProjectID = "project", body.ProjectID
	}
	ev := event.New(event.ThreadCreated, event.ActorUser, "", payload)
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
	var msgs []store.Message
	var err error
	if q := r.URL.Query(); q.Has("topic") {
		msgs, err = s.log.Store.TopicMessages(r.Context(), id, q.Get("topic"), before, limit)
	} else {
		msgs, err = s.log.Store.Messages(r.Context(), id, before, limit)
	}
	if err != nil {
		internal(w, err)
		return
	}
	last, _ := s.log.Store.LastSeq(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "last_seq": last})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	thread, err := s.log.Store.Thread(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "对话不存在")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	if thread.Kind == event.KindMeeting && thread.MeetingStatus == event.MeetingClosedState {
		writeError(w, http.StatusConflict, "meeting_closed", "会议已经结束，续会后才能发言")
		return
	}
	var body struct {
		Content     string                `json:"content"`
		Context     *event.MessageContext `json:"context"`
		Attachments []string              `json:"attachments"`
		// TopicID continues a topic; empty starts a new one.
		TopicID string `json:"topic_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	content := strings.TrimSpace(body.Content)
	atts, err := s.files.Resolve(body.Attachments)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_attachment", err.Error())
		return
	}
	if content == "" && len(atts) == 0 {
		writeError(w, http.StatusBadRequest, "empty", "消息不能为空")
		return
	}
	if utf8.RuneCountInString(content) > maxMessageRunes {
		writeError(w, http.StatusBadRequest, "too_long", "消息太长")
		return
	}
	if body.Context != nil {
		body.Context.Mentions = cleanMentions(body.Context.Mentions)
		if body.Context.Empty() {
			body.Context = nil
		}
	}
	if body.TopicID != "" {
		if t, err := s.log.Store.Topic(r.Context(), body.TopicID); errors.Is(err, store.ErrNotFound) || (err == nil && t.ThreadID != id) {
			writeError(w, http.StatusNotFound, "not_found", "话题不存在")
			return
		} else if err != nil {
			internal(w, err)
			return
		}
	}
	var noteIDs []string
	if body.Context != nil {
		seen := map[string]bool{}
		for _, noteID := range body.Context.NoteIDs {
			if seen[noteID] {
				continue
			}
			seen[noteID] = true
			if len(noteIDs) >= 20 {
				writeError(w, http.StatusBadRequest, "too_many", "笔记太多")
				return
			}
			if _, err := s.log.Store.Note(r.Context(), noteID); errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "笔记不存在")
				return
			} else if err != nil {
				internal(w, err)
				return
			}
			noteIDs = append(noteIDs, noteID)
		}
		body.Context.NoteIDs = noteIDs
	}
	msgID := ids.New()
	topicID := body.TopicID
	if topicID == "" {
		if thread.Kind == event.KindMeeting {
			topicID = thread.MeetingTopic
		} else {
			topicID = msgID
		}
	}
	evs, err := s.log.Write(r.Context(), func(w *store.Writer) error {
		if _, err := w.Append(event.New(event.MessageUser, event.ActorUser, id, event.MessagePayload{MessageID: msgID, Content: content, Context: body.Context, Attachments: atts, TopicID: body.TopicID})); err != nil {
			return err
		}
		if len(noteIDs) > 0 {
			_, err := w.Append(event.New(event.NoteSent, event.ActorUser, id, event.NoteSentPayload{NoteIDs: noteIDs, ThreadID: id, TopicID: topicID, MessageID: msgID}))
			return err
		}
		return nil
	})
	if err != nil {
		internal(w, err)
		return
	}
	topic := topicID
	writeJSON(w, http.StatusAccepted, map[string]any{"message_id": msgID, "seq": evs[0].Seq, "topic_id": topic})
}

// cleanMentions drops @ references without a project id and repeats, and
// keeps at most 16 with names cut to 100 characters.
func cleanMentions(in []event.Mention) []event.Mention {
	var out []event.Mention
	seen := map[string]bool{}
	for _, m := range in {
		m.ProjectID = strings.TrimSpace(m.ProjectID)
		if m.ProjectID == "" || seen[m.ProjectID] || len(out) >= 16 {
			continue
		}
		seen[m.ProjectID] = true
		if r := []rune(m.Name); len(r) > 100 {
			m.Name = string(r[:100])
		}
		out = append(out, m)
	}
	return out
}

// uploadAttachment stores the request body as an attachment; the name comes
// in ?name=. A message refers to the returned id.
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	a, err := s.files.Save(r.Body, r.URL.Query().Get("name"))
	switch {
	case errors.Is(err, attach.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "文件太大，最多 20 MB")
	case err != nil:
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "文件太大，最多 20 MB")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_file", err.Error())
	default:
		writeJSON(w, http.StatusCreated, a)
	}
}

// getAttachment serves a stored file. Pictures show inline; everything else
// downloads, so a file can never run as a page.
func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request) {
	a, err := s.files.Meta(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "附件不存在")
		return
	}
	f, err := s.files.Open(a.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "附件不存在")
		return
	}
	defer f.Close()
	if attach.IsImage(a.Mime) {
		w.Header().Set("Content-Type", a.Mime)
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 30
	}
	loc := time.Local
	if tz := r.URL.Query().Get("tz"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	now := time.Now().In(loc)
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1-days)
	rows, err := s.log.Store.UsageSince(r.Context(), since)
	if err != nil {
		internal(w, err)
		return
	}
	daily, err := s.log.Store.UsageDaily(r.Context(), since, loc)
	if err != nil {
		internal(w, err)
		return
	}
	byProject, err := s.log.Store.UsageByProject(r.Context(), since)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "since": since.UnixMilli(), "usage": rows, "daily": daily, "by_project": byProject})
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

// DeviceRoutes serves the conversation to paired machines, authenticated by
// wrap (their device token): what `sous chat` needs and nothing more.
func (s *Server) DeviceRoutes(wrap func(http.HandlerFunc) http.HandlerFunc) {
	s.mux.HandleFunc("GET /api/v1/device/threads", wrap(s.listThreads))
	s.mux.HandleFunc("POST /api/v1/device/threads", wrap(s.createThread))
	s.mux.HandleFunc("GET /api/v1/device/threads/{id}/messages", wrap(s.listMessages))
	s.mux.HandleFunc("POST /api/v1/device/threads/{id}/messages", wrap(s.postMessage))
	s.mux.HandleFunc("GET /api/v1/device/ws", wrap(s.deviceWS))
}

// Mux lets other packages add routes after New.
func (s *Server) Mux() *http.ServeMux { return s.mux }

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

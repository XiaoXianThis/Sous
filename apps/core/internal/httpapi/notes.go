package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func (s *Server) noteRoutes(protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/notes", s.listNotes)
	protect("POST /api/v1/notes", s.createNote)
	protect("PATCH /api/v1/notes/{id}", s.patchNote)
	protect("DELETE /api/v1/notes/{id}", s.deleteNote)
}
func (s *Server) listNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	notes, err := s.log.Store.Notes(r.Context(), store.NoteFilter{Status: q.Get("status"), ProjectID: q.Get("project_id"), Query: q.Get("q")})
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}
func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text        string   `json:"text"`
		ProjectID   string   `json:"project_id"`
		Attachments []string `json:"attachments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	atts, err := s.files.Resolve(b.Attachments)
	if err != nil {
		writeError(w, 400, "bad_attachment", err.Error())
		return
	}
	b.Text = strings.TrimSpace(b.Text)
	if b.Text == "" && len(atts) == 0 {
		writeError(w, 400, "empty", "笔记不能为空")
		return
	}
	if utf8.RuneCountInString(b.Text) > maxMessageRunes {
		writeError(w, 400, "too_long", "笔记太长")
		return
	}
	id := ids.New()
	p := event.NotePayload{NoteID: id, Text: &b.Text, ProjectID: &b.ProjectID, Attachments: atts}
	if _, err := s.log.Append(r.Context(), event.New(event.NotePut, event.ActorUser, "", p)); err != nil {
		internal(w, err)
		return
	}
	n, err := s.log.Store.Note(r.Context(), id)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 201, n)
}
func (s *Server) findNote(w http.ResponseWriter, r *http.Request) bool {
	_, err := s.log.Store.Note(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "not_found", "笔记不存在")
		return false
	}
	if err != nil {
		internal(w, err)
		return false
	}
	return true
}
func (s *Server) patchNote(w http.ResponseWriter, r *http.Request) {
	if !s.findNote(w, r) {
		return
	}
	var b struct {
		Text      *string `json:"text"`
		ProjectID *string `json:"project_id"`
		Status    *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	if b.Text != nil && utf8.RuneCountInString(*b.Text) > maxMessageRunes {
		writeError(w, 400, "too_long", "笔记太长")
		return
	}
	status := ""
	if b.Status != nil {
		status = *b.Status
		if status != event.NoteOpen && status != event.NoteSentDone && status != event.NoteArchived {
			writeError(w, 400, "bad_status", "无效的笔记状态")
			return
		}
	}
	id := r.PathValue("id")
	p := event.NotePayload{NoteID: id, Text: b.Text, ProjectID: b.ProjectID, Status: status}
	if _, err := s.log.Append(r.Context(), event.New(event.NotePut, event.ActorUser, "", p)); err != nil {
		internal(w, err)
		return
	}
	n, err := s.log.Store.Note(r.Context(), id)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, n)
}
func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	if !s.findNote(w, r) {
		return
	}
	if _, err := s.log.Append(r.Context(), event.New(event.NoteDeleted, event.ActorUser, "", event.NotePayload{NoteID: r.PathValue("id")})); err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

func (s *Server) memoryRoutes(protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/memories", s.listMemories)
	protect("POST /api/v1/memories", s.createMemory)
	protect("PUT /api/v1/memories/{id}", s.updateMemory)
	protect("DELETE /api/v1/memories/{id}", s.deleteMemory)
}

// listMemories searches with ?q=, or lists by ?kind= (newest first).
func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var ms []store.Memory
	var err error
	if q != "" {
		ms, err = s.log.Store.SearchMemories(r.Context(), q, limit)
	} else {
		ms, err = s.log.Store.Memories(r.Context(), r.URL.Query().Get("kind"), limit)
	}
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": ms, "kinds": tool.MemoryKinds})
}

func (s *Server) putMemory(w http.ResponseWriter, r *http.Request, id string, status int) {
	var body struct{ Kind, Content string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	content, err := tool.ValidateMemory(body.Kind, body.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	ev := event.New(event.MemoryPut, event.ActorUser, "", event.MemoryPayload{MemoryID: id, Kind: body.Kind, Content: content})
	if _, err := s.log.Append(r.Context(), ev); err != nil {
		internal(w, err)
		return
	}
	m, err := s.log.Store.Memory(r.Context(), id)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, status, m)
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request) {
	s.putMemory(w, r, ids.New(), http.StatusCreated)
}

func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.log.Store.Memory(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "记忆不存在")
		return
	}
	s.putMemory(w, r, id, http.StatusOK)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.log.Store.Memory(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "记忆不存在")
		return
	}
	ev := event.New(event.MemoryDeleted, event.ActorUser, "", event.MemoryPayload{MemoryID: id})
	if _, err := s.log.Append(r.Context(), ev); err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

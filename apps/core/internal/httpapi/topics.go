package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Topics of a thread (work/specs/chat-topics).

// TopicLimit is how many topics a list returns.
const TopicLimit = 200

// topicView is a topic as the chat's topic cards show it.
type topicView struct {
	store.Topic
	Count int            `json:"count"`
	Last  *store.Message `json:"last,omitempty"`
	Tasks []store.Task   `json:"tasks"`
}

func (s *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if _, err := s.log.Store.Thread(ctx, id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "对话不存在")
		return
	}
	topics, err := s.log.Store.Topics(ctx, id, TopicLimit)
	if err != nil {
		internal(w, err)
		return
	}
	stats, err := s.log.Store.TopicStats(ctx, id)
	if err != nil {
		internal(w, err)
		return
	}
	ids := make([]string, len(topics))
	for i, t := range topics {
		ids[i] = t.ID
	}
	tasks, err := s.log.Store.TopicTasks(ctx, ids)
	if err != nil {
		internal(w, err)
		return
	}
	out := make([]topicView, len(topics))
	for i, t := range topics {
		st := stats[t.ID]
		out[i] = topicView{Topic: t, Count: st.Count, Last: st.Last, Tasks: tasks[t.ID]}
		if out[i].Tasks == nil {
			out[i].Tasks = []store.Task{}
		}
	}
	// Messages from before topics are one group of their own.
	legacy := stats[""]
	writeJSON(w, http.StatusOK, map[string]any{"topics": out, "earlier": map[string]any{"count": legacy.Count, "last": legacy.Last}})
}

func (s *Server) getTopic(w http.ResponseWriter, r *http.Request) {
	t, err := s.log.Store.Topic(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "话题不存在")
		return
	} else if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) patchTopic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := s.log.Store.Topic(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "话题不存在")
		return
	} else if err != nil {
		internal(w, err)
		return
	}
	var body struct {
		Title  *string `json:"title"`
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	p := event.TopicUpdatedPayload{TopicID: t.ID}
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "empty", "标题不能为空")
			return
		}
		p.Title = &title
	}
	if body.Status != nil {
		if *body.Status != event.TopicOpen && *body.Status != event.TopicClosed {
			writeError(w, http.StatusBadRequest, "bad_status", "status 只能是 open 或 closed")
			return
		}
		p.Status = body.Status
	}
	if p.Title == nil && p.Status == nil {
		writeError(w, http.StatusBadRequest, "empty", "没有要改的")
		return
	}
	if _, err := s.log.Append(ctx, event.New(event.TopicUpdated, event.ActorUser, t.ThreadID, p)); err != nil {
		internal(w, err)
		return
	}
	t, err = s.log.Store.Topic(ctx, t.ID)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// moveMessage takes a message the supervisor attached to a topic back out
// into the topic it started; the supervisor then answers it on its own.
func (s *Server) moveMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := s.log.Store.MessageByID(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "消息不存在")
		return
	} else if err != nil {
		internal(w, err)
		return
	}
	var body struct {
		TopicID string `json:"topic_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if m.Role != "user" || body.TopicID != m.ID {
		writeError(w, http.StatusBadRequest, "bad_move", "只能把并入的消息移回它自己的话题")
		return
	}
	if m.TopicID == m.ID {
		writeJSON(w, http.StatusOK, m) // already there
		return
	}
	if _, err := s.log.Store.Topic(ctx, m.ID); err != nil {
		writeError(w, http.StatusBadRequest, "bad_move", "这条消息没有自己的话题")
		return
	}
	ev := event.New(event.MessageMoved, event.ActorUser, m.ThreadID, event.MessageMovedPayload{MessageID: m.ID, TopicID: m.ID, From: m.TopicID})
	if _, err := s.log.Append(ctx, ev); err != nil {
		internal(w, err)
		return
	}
	m, err = s.log.Store.Message(ctx, m.ThreadID, m.ID)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

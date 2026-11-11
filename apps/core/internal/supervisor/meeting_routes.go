package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func (s *Supervisor) meetingRoutes(protect func(string, http.HandlerFunc)) {
	protect("POST /api/v1/meetings", s.createMeeting)
	protect("GET /api/v1/meetings", s.listMeetings)
	protect("GET /api/v1/meetings/{id}", s.getMeeting)
	protect("POST /api/v1/meetings/{id}/close", s.closeMeeting)
	protect("POST /api/v1/meetings/{id}/reopen", s.reopenMeeting)
	protect("POST /api/v1/meetings/{id}/minutes", s.sendMinutes)
	protect("PATCH /api/v1/meetings/{id}", s.patchMeeting)
}
func (s *Supervisor) createMeeting(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ParentThreadID string             `json:"parent_thread_id"`
		ParentTopicID  string             `json:"parent_topic_id"`
		Question       string             `json:"question"`
		ProjectID      string             `json:"project_id"`
		NoteIDs        []string           `json:"note_ids"`
		Host           *event.MeetingHost `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	b.Question = strings.TrimSpace(b.Question)
	if b.Question == "" {
		writeError(w, 400, "empty", "会议议题不能为空")
		return
	}
	ctx := r.Context()
	st := s.opt.Log.Store
	parent, err := st.Thread(ctx, b.ParentThreadID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "not_found", "对话不存在")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	var topic store.Topic
	if b.ParentTopicID != "" {
		topic, err = st.Topic(ctx, b.ParentTopicID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && topic.ThreadID != parent.ID) {
			writeError(w, 404, "not_found", "话题不存在")
			return
		}
		if err != nil {
			internal(w, err)
			return
		}
	}
	project := b.ProjectID
	if project == "" {
		project = parent.ProjectID
	}
	origin := "主线"
	if parent.Kind == "project" {
		p, e := st.Project(ctx, parent.ProjectID)
		if e != nil {
			internal(w, e)
			return
		}
		origin = p.Name + " 的项目对话"
	}
	var brief strings.Builder
	fmt.Fprintf(&brief, "## 来自\n%s（%s）\n", parent.Title, origin)
	if topic.ID != "" {
		summary := topic.Summary
		if summary == "" {
			summary = "还没有结论"
		}
		fmt.Fprintf(&brief, "## 话题\n%s：%s\n", topic.Title, summary)
		tasks, e := st.Tasks(ctx, store.TaskFilter{ThreadID: parent.ID})
		if e != nil {
			internal(w, e)
			return
		}
		var lines []string
		for _, t := range tasks {
			if t.TopicID == topic.ID {
				lines = append(lines, fmt.Sprintf("- %s「%s」%s", t.ID, t.Title, t.Status))
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&brief, "## 相关任务\n%s\n", strings.Join(lines, "\n"))
		}
		seg, e := st.LatestSegment(ctx, parent.ID)
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			internal(w, e)
			return
		}
		msgs, e := st.TopicMessages(ctx, parent.ID, topic.ID, 0, 100000)
		if e != nil {
			internal(w, e)
			return
		}
		refs := map[string]bool{}
		for _, m := range msgs {
			refs[fmt.Sprintf("[#%d]", m.Seq)] = true
		}
		var matches []string
		for _, line := range strings.Split(seg.Summary, "\n") {
			for _, ref := range refRe.FindAllString(line, -1) {
				if refs[ref] {
					matches = append(matches, line)
					break
				}
			}
		}
		if len(matches) > 0 {
			fmt.Fprintf(&brief, "## 相关摘要\n%s\n", strings.Join(matches, "\n"))
		}
	}
	seen := map[string]bool{}
	var notes []store.Note
	for _, id := range b.NoteIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(notes) >= 20 {
			writeError(w, 400, "too_many", "笔记太多")
			return
		}
		n, e := st.Note(ctx, id)
		if errors.Is(e, store.ErrNotFound) {
			writeError(w, 404, "not_found", "笔记不存在")
			return
		}
		if e != nil {
			internal(w, e)
			return
		}
		notes = append(notes, n)
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].CreatedAt < notes[j].CreatedAt })
	noteIDs := make([]string, 0, len(notes))
	var texts []string
	for _, n := range notes {
		noteIDs = append(noteIDs, n.ID)
		texts = append(texts, n.Text)
	}
	if len(texts) > 0 {
		fmt.Fprintf(&brief, "## 笔记\n%s\n", strings.Join(texts, "\n\n"))
	}
	id, openID := ids.New(), ids.New()
	title := store.TopicTitle("", b.Question)
	payload := event.ThreadCreatedPayload{Title: title, Kind: event.KindMeeting, ProjectID: project, ParentThreadID: parent.ID, ParentTopicID: topic.ID, MeetingTopic: openID, Brief: strings.TrimSpace(brief.String()), Host: b.Host}
	_, err = s.opt.Log.Write(ctx, func(wr *store.Writer) error {
		if _, e := wr.Append(event.New(event.ThreadCreated, event.ActorUser, id, payload)); e != nil {
			return e
		}
		if _, e := wr.Append(event.New(event.MessageUser, event.ActorUser, id, event.MessagePayload{MessageID: openID, Content: b.Question, TopicTitle: title, Context: &event.MessageContext{ProjectID: project, NoteIDs: noteIDs}})); e != nil {
			return e
		}
		if len(noteIDs) > 0 {
			_, e := wr.Append(event.New(event.NoteSent, event.ActorUser, id, event.NoteSentPayload{NoteIDs: noteIDs, ThreadID: id, TopicID: openID, MessageID: openID}))
			return e
		}
		return nil
	})
	if err != nil {
		internal(w, err)
		return
	}
	m, err := st.Thread(ctx, id)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"meeting": m})
}
func (s *Supervisor) meeting(w http.ResponseWriter, r *http.Request) (store.Thread, bool) {
	m, e := s.opt.Log.Store.Thread(r.Context(), r.PathValue("id"))
	if errors.Is(e, store.ErrNotFound) || (e == nil && !isMeeting(m)) {
		writeError(w, 404, "not_found", "会议不存在")
		return m, false
	}
	if e != nil {
		internal(w, e)
		return m, false
	}
	return m, true
}
func (s *Supervisor) listMeetings(w http.ResponseWriter, r *http.Request) {
	v, e := s.opt.Log.Store.Meetings(r.Context(), r.URL.Query().Get("parent_thread_id"))
	if e != nil {
		internal(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"meetings": v})
}
func (s *Supervisor) getMeeting(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meeting(w, r)
	if !ok {
		return
	}
	v, e := s.opt.Log.Store.Minutes(r.Context(), m.ID)
	if e != nil {
		internal(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"meeting": m, "minutes": v})
}
func (s *Supervisor) closeMeeting(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meeting(w, r)
	if !ok {
		return
	}
	if m.MeetingStatus == event.MeetingClosedState {
		writeError(w, 409, "meeting_closed", "会议已经散会")
		return
	}
	if _, e := s.opt.Log.Append(r.Context(), event.New(event.MeetingClosed, event.ActorUser, m.ID, event.MeetingPayload{MeetingID: m.ID})); e != nil {
		internal(w, e)
		return
	}
	s.enqueue(m.ID, func() { s.writeMinutes(m.ID) })
	writeJSON(w, 202, map[string]bool{"queued": true})
}
func (s *Supervisor) reopenMeeting(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meeting(w, r)
	if !ok {
		return
	}
	if m.MeetingStatus != event.MeetingClosedState {
		writeError(w, 409, "meeting_open", "会议还在进行")
		return
	}
	if _, e := s.opt.Log.Append(r.Context(), event.New(event.MeetingReopened, event.ActorUser, m.ID, event.MeetingPayload{MeetingID: m.ID})); e != nil {
		internal(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Supervisor) sendMinutes(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meeting(w, r)
	if !ok {
		return
	}
	var b struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil && err.Error() != "EOF" {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	id, e := s.postMinutes(r.Context(), m.ID, b.Content)
	if errors.Is(e, errNoDraft) {
		writeError(w, 409, "no_draft", "没有纪要草稿")
		return
	}
	if e != nil {
		internal(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"message_id": id})
}
func (s *Supervisor) patchMeeting(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meeting(w, r)
	if !ok {
		return
	}
	if m.MeetingStatus == event.MeetingClosedState {
		writeError(w, 409, "meeting_closed", "会议已经散会")
		return
	}
	var b struct {
		Host *event.MeetingHost `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	if _, e := s.opt.Log.Append(r.Context(), event.New(event.MeetingHostSet, event.ActorUser, m.ID, event.MeetingPayload{MeetingID: m.ID, Host: b.Host})); e != nil {
		internal(w, e)
		return
	}
	m, e := s.opt.Log.Store.Thread(r.Context(), m.ID)
	if e != nil {
		internal(w, e)
		return
	}
	writeJSON(w, 200, m)
}

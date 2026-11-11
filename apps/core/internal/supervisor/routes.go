package supervisor

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// maxSummaryRunes caps a summary the user writes.
const maxSummaryRunes = 20000

// cacheCalls is how many recent calls the cache hit rate covers.
const cacheCalls = 20

// ContextView is what the page shows about a thread's context.
type ContextView struct {
	Segment     int      `json:"segment"`
	CoversUpTo  int64    `json:"covers_up_to"`
	Summary     string   `json:"summary"`
	Profile     []string `json:"profile"`
	Reason      string   `json:"reason,omitempty"`
	CompactedAt int64    `json:"compacted_at,omitempty"`
	// Used is the estimated input tokens of the thread's next turn.
	Used   int `json:"used"`
	Window int `json:"window"`
	Soft   int `json:"soft"`
	Hard   int `json:"hard"`
	// Cache sums the thread's recent supervisor calls.
	Cache store.ThreadUsage `json:"cache"`
}

// Routes serves a thread's context: GET .../context, POST .../compact and
// PUT .../summary (docs/api/core-hub-http.md).
func (s *Supervisor) Routes(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
	s.meetingRoutes(protect)
	protect("GET /api/v1/threads/{id}/context", s.getContext)
	protect("POST /api/v1/threads/{id}/compact", s.postCompact)
	protect("PUT /api/v1/threads/{id}/summary", s.putSummary)
}

func (s *Supervisor) getContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := s.opt.Log.Store
	thread, ok := s.thread(w, r)
	if !ok {
		return
	}
	seg, err := st.LatestSegment(ctx, thread.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internal(w, err)
		return
	}
	history, err := st.MessagesAfter(ctx, thread.ID, seg.CoversUpTo)
	if err != nil {
		internal(w, err)
		return
	}
	window := s.window(ctx)
	v := ContextView{Segment: seg.Segment, CoversUpTo: seg.CoversUpTo, Summary: seg.Summary, Profile: seg.Profile,
		Reason: seg.Reason, CompactedAt: seg.CreatedAt,
		Used: estimateRequest(s.build(ctx, thread, seg, history).req), Window: window,
		Soft: int(SoftRatio * float64(window)), Hard: int(HardRatio * float64(window))}
	if v.Profile == nil {
		v.Profile = []string{}
	}
	if v.Cache, err = st.RecentThreadUsage(ctx, thread.ID, "supervisor", cacheCalls); err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Supervisor) postCompact(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.thread(w, r)
	if !ok {
		return
	}
	s.Compact(thread.ID)
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (s *Supervisor) putSummary(w http.ResponseWriter, r *http.Request) {
	thread, ok := s.thread(w, r)
	if !ok {
		return
	}
	var body struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if utf8.RuneCountInString(body.Summary) > maxSummaryRunes {
		writeError(w, http.StatusBadRequest, "invalid", "摘要太长了（最多 20000 字）")
		return
	}
	s.EditSummary(thread.ID, strings.TrimSpace(body.Summary))
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (s *Supervisor) thread(w http.ResponseWriter, r *http.Request) (store.Thread, bool) {
	t, err := s.opt.Log.Store.Thread(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "对话不存在")
		return t, false
	}
	if err != nil {
		internal(w, err)
		return t, false
	}
	return t, true
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
	slog.Error("http: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

package nodehub

import (
	"context"
	"encoding/json"
	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"net/http"
	"strings"
)

type deviceKey struct{}

// DeviceAuth lets a paired machine call an endpoint with its device token
// (Authorization: Bearer). `sous chat` uses this through the daemon, so a
// terminal on a paired machine needs no passkey. Only the endpoints that
// docs/api/core-daemon-ipc.md lists are wrapped: reading threads, sending
// messages, the event stream and approvals; never settings or machines.
func (h *Hub) DeviceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if t, ok := auth.TunnelOf(r.Context()); ok && t.Device != "" {
			if n, err := h.log.Store.Node(r.Context(), t.Device); err != nil || n.Revoked {
				writeError(w, http.StatusUnauthorized, "unauthorized", "unknown machine")
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, t.Device)))
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing device token")
			return
		}
		n, err := h.log.Store.NodeByToken(r.Context(), hashToken(token))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "unknown or revoked device token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, n.ID)))
	}
}

// DeviceOf returns the machine a device-authenticated request came from.
func DeviceOf(ctx context.Context) string {
	id, _ := ctx.Value(deviceKey{}).(string)
	return id
}

// DeviceRoutes registers the approval endpoints for paired machines.
func (h *Hub) DeviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/device/notes", h.DeviceAuth(h.deviceNote))
	mux.HandleFunc("GET /api/v1/device/approvals", h.DeviceAuth(h.listApprovals))
	mux.HandleFunc("POST /api/v1/device/approvals/{id}/decide", h.DeviceAuth(h.decide))
}

func (h *Hub) deviceNote(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	b.Text = strings.TrimSpace(b.Text)
	if b.Text == "" {
		writeError(w, 400, "empty", "笔记不能为空")
		return
	}
	if utf8.RuneCountInString(b.Text) > 20000 {
		writeError(w, 400, "too_long", "笔记太长")
		return
	}
	id := ids.New()
	if _, err := h.log.Append(r.Context(), event.New(event.NotePut, event.ActorUser, "", event.NotePayload{NoteID: id, Text: &b.Text})); err != nil {
		writeError(w, 500, "internal", err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"note_id": id})
}

package nodehub

import (
	"context"
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
	mux.HandleFunc("GET /api/v1/device/approvals", h.DeviceAuth(h.listApprovals))
	mux.HandleFunc("POST /api/v1/device/approvals/{id}/decide", h.DeviceAuth(h.decide))
}

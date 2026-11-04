package update

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

// Nodes is what the update routes need from the node hub.
type Nodes interface {
	Online(nodeID string) bool
	RequestUpdate(ctx context.Context, nodeID, version, repo string) error
}

// Routes serves GET /api/v1/updates and POST /api/v1/nodes/{id}/update.
func (c *Checker) Routes(st *store.Store, nodes Nodes) func(*http.ServeMux, func(string, http.HandlerFunc)) {
	return func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		protect("GET /api/v1/updates", func(w http.ResponseWriter, r *http.Request) {
			rel, at, err := c.Latest(r.Context(), r.URL.Query().Get("refresh") == "1")
			out := map[string]any{"hub": map[string]any{"version": version.Version}, "checked_at": at.UnixMilli(), "repo": c.Client.repo()}
			if err != nil {
				out["error"] = err.Error()
			} else {
				out["latest"] = map[string]any{"tag": rel.Tag, "name": rel.Name, "url": rel.HTMLURL, "published_at": rel.PublishedAt, "notes": rel.Body}
				out["hub"] = map[string]any{"version": version.Version, "update_available": Newer(rel.Tag, version.Version)}
			}
			ns, _ := st.Nodes(r.Context())
			list := []map[string]any{}
			for _, n := range ns {
				list = append(list, map[string]any{
					"id": n.ID, "name": n.Name, "version": n.Version, "online": nodes.Online(n.ID), "os": n.OS, "arch": n.Arch,
					"update_available": err == nil && Newer(rel.Tag, n.Version),
				})
			}
			out["nodes"] = list
			writeJSON(w, http.StatusOK, out)
		})
		protect("POST /api/v1/nodes/{id}/update", func(w http.ResponseWriter, r *http.Request) {
			var b struct {
				Version string `json:"version"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			id := r.PathValue("id")
			if _, err := st.Node(r.Context(), id); err != nil {
				writeError(w, http.StatusNotFound, "not_found", "机器不存在")
				return
			}
			v := strings.TrimSpace(b.Version)
			if v == "" {
				rel, _, err := c.Latest(r.Context(), false)
				if err != nil {
					writeError(w, http.StatusConflict, "no_release", err.Error())
					return
				}
				v = rel.Tag
			}
			if err := nodes.RequestUpdate(r.Context(), id, v, c.Client.repo()); err != nil {
				writeError(w, http.StatusConflict, "offline", err.Error())
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]string{"version": v})
		})
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

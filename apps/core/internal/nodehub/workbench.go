package nodehub

import (
	"context"
	"encoding/json"

	"net/http"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Milestone M5: a machine's role and workspaces, the repositories found in
// them, and pull request states for deploy-on-merge.

func (h *Hub) workbenchRoutes(protect func(string, http.HandlerFunc)) {
	protect("PUT /api/v1/nodes/{id}/role", h.putRole)
	protect("PUT /api/v1/nodes/{id}/workspaces", h.putWorkspaces)
	protect("POST /api/v1/nodes/{id}/scan", h.scanNode)
	protect("GET /api/v1/repos", h.listRepos)
}

// SetRole asks a machine to change its role. A server can only become a
// dev machine again by editing its node.json.
func (h *Hub) SetRole(ctx context.Context, nodeID, role string) error {
	_, err := h.Request(ctx, nodeID, proto.OpSetRole, proto.RoleArgs{Role: role}, requestTimeout)
	return err
}

// SetWorkspaces replaces a machine's workspaces; it scans them at once.
func (h *Hub) SetWorkspaces(ctx context.Context, nodeID string, ws []string) (proto.NodeRepos, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpSetWorkspaces, proto.WorkspacesArgs{Workspaces: ws}, detectTimeout)
	if err != nil {
		return proto.NodeRepos{}, err
	}
	var nr proto.NodeRepos
	err = json.Unmarshal(raw, &nr)
	return nr, err
}

// PRState asks a machine (with gh) whether a pull request was merged.
func (h *Hub) PRState(ctx context.Context, nodeID, url string) (proto.PRState, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpPRState, proto.PRArgs{URL: url}, time.Minute)
	if err != nil {
		return proto.PRState{}, err
	}
	var st proto.PRState
	err = json.Unmarshal(raw, &st)
	return st, err
}

// CanRequest reports whether a machine is online and answers node.request.
func (h *Hub) CanRequest(nodeID string) bool { return h.AdminState(nodeID) == "on" }

func (h *Hub) putRole(w http.ResponseWriter, r *http.Request) {
	var body struct{ Role string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if err := h.SetRole(r.Context(), r.PathValue("id"), body.Role); err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": body.Role})
}

func (h *Hub) putWorkspaces(w http.ResponseWriter, r *http.Request) {
	var body struct{ Workspaces []string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	nr, err := h.SetWorkspaces(r.Context(), r.PathValue("id"), body.Workspaces)
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nr)
}

func (h *Hub) scanNode(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Request(r.Context(), r.PathValue("id"), proto.OpScan, struct{}{}, detectTimeout)
	if err != nil {
		adminError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (h *Hub) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := h.log.Store.RepoScans(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

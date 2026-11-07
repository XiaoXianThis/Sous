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

// GitStatus reads where a project checkout stands on a machine, right now
// (decision 0022).
func (h *Hub) GitStatus(ctx context.Context, nodeID, root string) (proto.GitStatus, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpGitStatus, proto.RepoArgs{Root: root}, time.Minute)
	if err != nil {
		return proto.GitStatus{}, err
	}
	var st proto.GitStatus
	err = json.Unmarshal(raw, &st)
	return st, err
}

// Grep searches a project directory on a machine.
func (h *Hub) Grep(ctx context.Context, nodeID string, args proto.GrepArgs) (proto.GrepResult, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpProjectGrep, args, time.Minute)
	if err != nil {
		return proto.GrepResult{}, err
	}
	var res proto.GrepResult
	err = json.Unmarshal(raw, &res)
	return res, err
}

// Look runs one read-only command on a machine (decision 0024).
func (h *Hub) Look(ctx context.Context, nodeID string, args proto.LookArgs) (proto.LookResult, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpLook, args, time.Minute)
	if err != nil {
		return proto.LookResult{}, err
	}
	var res proto.LookResult
	err = json.Unmarshal(raw, &res)
	return res, err
}

// ProjectFile reads one file of a project directory on a machine.
func (h *Hub) ProjectFile(ctx context.Context, nodeID string, args proto.ProjectArgs) (proto.ProjectFile, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpProjectRead, args, time.Minute)
	if err != nil {
		return proto.ProjectFile{}, err
	}
	var f proto.ProjectFile
	err = json.Unmarshal(raw, &f)
	return f, err
}

// ProjectDir lists one directory of a project on a machine.
func (h *Hub) ProjectDir(ctx context.Context, nodeID string, args proto.ProjectArgs) (proto.ProjectDir, error) {
	raw, err := h.Request(ctx, nodeID, proto.OpProjectLs, args, time.Minute)
	if err != nil {
		return proto.ProjectDir{}, err
	}
	var d proto.ProjectDir
	err = json.Unmarshal(raw, &d)
	return d, err
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

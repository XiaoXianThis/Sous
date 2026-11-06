package nodehub

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Routes registers the machine, run and approval endpoints. The pairing
// endpoint and /node authenticate with codes and device tokens instead of a
// user session.
func (h *Hub) Routes(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
	mux.HandleFunc("POST /api/v1/node/pair", h.handlePair)
	mux.HandleFunc("GET /node", h.ServeNode)

	protect("GET /api/v1/nodes", h.listNodes)
	protect("POST /api/v1/nodes/pairing-code", h.pairingCode)
	protect("PATCH /api/v1/nodes/{id}", h.renameNode)
	protect("DELETE /api/v1/nodes/{id}", h.revokeNode)
	h.adminRoutes(protect)
	h.workbenchRoutes(protect)

	protect("GET /api/v1/runs", h.listRuns)
	protect("POST /api/v1/runs", h.createRun)
	protect("GET /api/v1/runs/{id}", h.getRun)
	protect("GET /api/v1/runs/{id}/log", h.runFile(h.LogPath))
	protect("GET /api/v1/runs/{id}/diff", h.runFile(h.DiffPath))
	protect("POST /api/v1/runs/{id}/cancel", h.cancelRun)
	protect("POST /api/v1/runs/{id}/message", h.messageRun)
	protect("POST /api/v1/runs/{id}/pause", h.pauseRun)
	protect("POST /api/v1/runs/{id}/resume", h.resumeRun)

	protect("GET /api/v1/approvals", h.listApprovals)
	protect("POST /api/v1/approvals/{id}/decide", h.decide)
}

func (h *Hub) handlePair(w http.ResponseWriter, r *http.Request) {
	var req proto.PairRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	resp, err := h.Pair(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusForbidden, "pairing", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Hub) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.log.Store.Nodes(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	for i := range nodes {
		nodes[i].Online = h.Online(nodes[i].ID) // live state beats the stored flag
		nodes[i].Admin = h.AdminState(nodes[i].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (h *Hub) pairingCode(w http.ResponseWriter, r *http.Request) {
	code, exp := h.NewPairingCode()
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires_at": exp.UnixMilli()})
}

func (h *Hub) renameNode(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid", "需要新名字")
		return
	}
	id := r.PathValue("id")
	if _, err := h.log.Store.Node(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "机器不存在")
		return
	}
	if _, err := h.log.Append(r.Context(), event.New(event.NodeUpdated, event.ActorUser, "", event.NodePayload{NodeID: id, Name: strings.TrimSpace(body.Name)})); err != nil {
		internal(w, err)
		return
	}
	n, _ := h.log.Store.Node(r.Context(), id)
	n.Online = h.Online(id)
	writeJSON(w, http.StatusOK, n)
}

func (h *Hub) revokeNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if n, err := h.log.Store.Node(r.Context(), id); err != nil || n.Revoked {
		writeError(w, http.StatusNotFound, "not_found", "机器不存在")
		return
	}
	if err := h.Revoke(r.Context(), id); err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Hub) listRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.RunFilter{ThreadID: q.Get("thread_id"), NodeID: q.Get("node_id")}
	if s := q.Get("status"); s != "" {
		f.Statuses = strings.Split(s, ",")
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	runs, err := h.log.Store.Runs(r.Context(), f)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *Hub) createRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ThreadID string `json:"thread_id"`
		NodeID   string `json:"node_id"`
		Agent    string `json:"agent"`
		RepoPath string `json:"repo_path"`
		Prompt   string `json:"prompt"`
		// A run in a repository works on its own branch, which starts where
		// these say; there is no default (decision 0022).
		BaseBranch string `json:"base_branch"`
		BaseFrom   string `json:"base_from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.RepoPath != "" && (body.BaseBranch == "" || (body.BaseFrom != "local" && body.BaseFrom != "remote")) {
		writeError(w, http.StatusBadRequest, "run", "在仓库里执行要说明从哪个分支开始：base_branch 和 base_from（local 或 remote）")
		return
	}
	run, err := h.Create(r.Context(), CreateRun{ThreadID: body.ThreadID, NodeID: body.NodeID, Agent: body.Agent, RepoPath: body.RepoPath, Prompt: body.Prompt,
		BaseBranch: body.BaseBranch, BaseFrom: body.BaseFrom})
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrNoNode) {
			code = http.StatusConflict
		}
		writeError(w, code, "run", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

// getRun returns the run, its stored events and its approvals.
func (h *Hub) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.log.Store.Run(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Run 不存在")
		return
	} else if err != nil {
		internal(w, err)
		return
	}
	evs, err := h.log.Store.RunEvents(r.Context(), id)
	if err != nil {
		internal(w, err)
		return
	}
	approvals, err := h.log.Store.Approvals(r.Context(), "", id)
	if err != nil {
		internal(w, err)
		return
	}
	if evs == nil {
		evs = []event.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "events": evs, "approvals": approvals})
}

func (h *Hub) runFile(path func(string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := h.log.Store.Run(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "not_found", "Run 不存在")
			return
		}
		f, err := h.openRunFile(path(id))
		if errors.Is(err, os.ErrNotExist) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			return
		} else if err != nil {
			internal(w, err)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.Copy(w, f)
	}
}

func (h *Hub) cancelRun(w http.ResponseWriter, r *http.Request) {
	if err := h.Cancel(r.Context(), r.PathValue("id"), "用户取消"); err != nil {
		writeError(w, http.StatusConflict, "run", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (h *Hub) messageRun(w http.ResponseWriter, r *http.Request) {
	var body struct{ Text string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeError(w, http.StatusBadRequest, "invalid", "消息不能为空")
		return
	}
	if err := h.Message(r.Context(), r.PathValue("id"), strings.TrimSpace(body.Text)); err != nil {
		writeError(w, http.StatusConflict, "run", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (h *Hub) pauseRun(w http.ResponseWriter, r *http.Request) {
	if err := h.Pause(r.Context(), r.PathValue("id"), "用户暂停", event.ActorUser); err != nil {
		writeError(w, http.StatusConflict, "run", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (h *Hub) resumeRun(w http.ResponseWriter, r *http.Request) {
	var body struct{ Text string }
	json.NewDecoder(r.Body).Decode(&body)
	if err := h.Resume(r.Context(), r.PathValue("id"), strings.TrimSpace(body.Text), event.ActorUser); err != nil {
		writeError(w, http.StatusConflict, "run", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (h *Hub) listApprovals(w http.ResponseWriter, r *http.Request) {
	as, err := h.log.Store.Approvals(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("run_id"))
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": as})
}

func (h *Hub) decide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OptionID string `json:"option_id"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	err := h.Decide(r.Context(), r.PathValue("id"), body.OptionID, body.Note)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "审批不存在")
		return
	} else if err != nil {
		writeError(w, http.StatusConflict, "approval", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	slog.Error("nodehub: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

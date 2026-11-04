package task

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func (e *Engine) workbenchRoutes(protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/personas", e.listPersonas)
	protect("POST /api/v1/personas", e.putPersona)
	protect("PATCH /api/v1/personas/{id}", e.putPersona)
	protect("DELETE /api/v1/personas/{id}", e.deletePersona)

	protect("POST /api/v1/projects/new", e.newProject)
	protect("POST /api/v1/projects/{id}/clone", e.cloneProject)

	protect("GET /api/v1/projects/{id}/tree", e.projectFiles(proto.OpProjectLs))
	protect("GET /api/v1/projects/{id}/file", e.projectFiles(proto.OpProjectRead))
	protect("PUT /api/v1/projects/{id}/file", e.projectFiles(proto.OpProjectWrite))

	protect("POST /api/v1/projects/{id}/locations/{node}/sync", e.repoOp(proto.OpRepoSync))
	protect("POST /api/v1/projects/{id}/locations/{node}/wip", e.repoOp(proto.OpRepoWIP))
	protect("GET /api/v1/projects/{id}/shells", e.listShells)
	protect("POST /api/v1/projects/{id}/shells", e.openShell)

	protect("GET /api/v1/overview", e.overview)
}

// repoOp brings a project's checkout on one machine up to date with its
// upstream (fast-forward only), or saves its uncommitted work to a
// sous/wip/ branch on the remote (work/specs/multi-machine-sync, R3/R4).
func (e *Engine) repoOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := e.store().Project(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "项目不存在")
			return
		}
		nodeID := r.PathValue("node")
		root := locationRoot(p, nodeID)
		if root == "" {
			writeError(w, http.StatusBadRequest, "invalid", "这个项目在那台机器上没有目录")
			return
		}
		res, err := e.Nodes.Request(r.Context(), nodeID, op, proto.RepoArgs{Root: root}, 3*time.Minute)
		if err != nil {
			nodehub.WriteAdminError(w, err)
			return
		}
		var out proto.RepoResult
		json.Unmarshal(res, &out)
		what := map[string]string{proto.OpRepoSync: "同步", proto.OpRepoWIP: "存档"}[op]
		e.Log.Append(r.Context(), event.New(event.ProjectRepoOp, event.ActorUser, "", event.ProjectRepoOpPayload{
			ProjectID: p.ID, NodeID: nodeID, Path: root, Op: op, Outcome: out.Outcome, Message: what + "：" + out.Message, Branch: out.Branch}))
		writeJSON(w, http.StatusOK, out)
	}
}

// locationRoot is the project's directory on a machine, or "".
func locationRoot(p store.Project, nodeID string) string {
	for _, pn := range p.Nodes {
		if pn.NodeID == nodeID && pn.Path != "" {
			return pn.Path
		}
	}
	return ""
}

// openShell opens a terminal in the project's directory on one of its
// machines (body: node_id). It needs a recent passkey login, and the shell
// belongs to the login session that opened it. Typing into it, resizing,
// reading and closing use the machine's job endpoints.
func (e *Engine) openShell(w http.ResponseWriter, r *http.Request) {
	var b struct {
		NodeID string `json:"node_id"`
		Cols   int    `json:"cols"`
		Rows   int    `json:"rows"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	p, err := e.store().Project(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "项目不存在")
		return
	}
	root := locationRoot(p, b.NodeID)
	if root == "" {
		writeError(w, http.StatusBadRequest, "invalid", "这个项目在那台机器上没有目录")
		return
	}
	if !e.Nodes.RequireFresh(w, r) {
		return
	}
	owner := e.Nodes.SessionOwner(r)
	res, err := e.Nodes.Request(r.Context(), b.NodeID, proto.OpShellOpen,
		proto.ShellArgs{Root: root, Owner: owner, Cols: b.Cols, Rows: b.Rows}, 20*time.Second)
	if err != nil {
		nodehub.WriteAdminError(w, err)
		return
	}
	var js proto.JobStarted
	if err := json.Unmarshal(res, &js); err != nil || js.JobID == "" {
		writeError(w, http.StatusBadGateway, "failed", "机器的回答不对")
		return
	}
	// Recorded for the audit trail; what is typed and printed is not kept.
	if _, err := e.Log.Append(r.Context(), event.New(event.AgentJobStarted, event.ActorUser, "", event.AgentJobPayload{
		NodeID: b.NodeID, JobID: js.JobID, Kind: js.Kind, Command: js.Command, Path: root, ProjectID: p.ID})); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node_id": b.NodeID, "job_id": js.JobID, "command": js.Command, "root": root})
}

// listShells returns the request's session's shells in this project, on
// every machine of it that is online.
func (e *Engine) listShells(w http.ResponseWriter, r *http.Request) {
	p, err := e.store().Project(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "项目不存在")
		return
	}
	type shell struct {
		NodeID string `json:"node_id"`
		proto.ShellInfo
	}
	out := []shell{}
	owner := e.Nodes.SessionOwner(r)
	for _, pn := range p.Nodes {
		if pn.Path == "" || e.Nodes.AdminState(pn.NodeID) != "on" {
			continue
		}
		res, err := e.Nodes.Request(r.Context(), pn.NodeID, proto.OpShellList, proto.ShellArgs{Root: pn.Path, Owner: owner}, 10*time.Second)
		if err != nil {
			continue // an older or busy machine just shows no shells
		}
		var list []proto.ShellInfo
		json.Unmarshal(res, &list)
		for _, s := range list {
			out = append(out, shell{NodeID: pn.NodeID, ShellInfo: s})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shells": out})
}

// projectFiles browses and edits a project's files on one of its machines
// (?node=, else the first of them that is online).
func (e *Engine) projectFiles(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := e.store().Project(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "项目不存在")
			return
		}
		want := r.URL.Query().Get("node")
		var root, nodeID string
		for _, pn := range p.Nodes {
			if (want != "" && pn.NodeID != want) || pn.Path == "" {
				continue
			}
			if want != "" || e.Nodes.AdminState(pn.NodeID) != "" {
				root, nodeID = pn.Path, pn.NodeID
				break
			}
		}
		if nodeID == "" {
			writeError(w, http.StatusConflict, "offline", "这个项目所在的机器都不在线")
			return
		}
		args := proto.ProjectArgs{Root: root, Rel: r.URL.Query().Get("path")}
		if op == proto.OpProjectWrite {
			var b struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				BaseSHA256 string `json:"base_sha256"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&b); err != nil {
				writeError(w, http.StatusBadRequest, "bad_json", err.Error())
				return
			}
			args.Rel, args.Content, args.BaseSHA256 = b.Path, b.Content, b.BaseSHA256
		}
		res, err := e.Nodes.Request(r.Context(), nodeID, op, args, 20*time.Second)
		if err != nil {
			nodehub.WriteAdminError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := `{"node_id":` + strconv.Quote(nodeID) + `,"result":` + string(res) + `}`
		w.Write([]byte(out))
	}
}

func (e *Engine) listPersonas(w http.ResponseWriter, r *http.Request) {
	ps, err := e.store().Personas(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": ps})
}

func (e *Engine) putPersona(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name        string            `json:"name"`
		Glyph       string            `json:"glyph"`
		Color       string            `json:"color"`
		Agent       string            `json:"agent"`
		Options     map[string]string `json:"options"`
		Brief       string            `json:"brief"`
		DefaultMode string            `json:"default_mode"`
		Kinds       []string          `json:"kinds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	id := r.PathValue("id")
	if id != "" {
		if _, err := e.store().Persona(r.Context(), id); err != nil {
			taskError(w, err)
			return
		}
	}
	p, err := e.PutPersona(r.Context(), PersonaInput{ID: id, Name: b.Name, Glyph: b.Glyph, Color: b.Color, Agent: b.Agent,
		Options: b.Options, Brief: b.Brief, DefaultMode: b.DefaultMode, Kinds: b.Kinds}, event.ActorUser)
	if err != nil {
		taskError(w, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, p)
}

func (e *Engine) deletePersona(w http.ResponseWriter, r *http.Request) {
	if err := e.DeletePersona(r.Context(), r.PathValue("id"), event.ActorUser); err != nil {
		taskError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e *Engine) newProject(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name     string `json:"name"`
		NodeID   string `json:"node_id"`
		Layout   string `json:"layout"`
		Owner    string `json:"owner"`
		Private  bool   `json:"private"`
		ThreadID string `json:"thread_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	p, run, err := e.CreateProject(r.Context(), SetupInput{Name: b.Name, NodeID: b.NodeID, Layout: b.Layout, Owner: b.Owner,
		Private: b.Private, ThreadID: b.ThreadID, Actor: event.ActorUser})
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": p, "run": run})
}

func (e *Engine) cloneProject(w http.ResponseWriter, r *http.Request) {
	var b struct {
		NodeID   string `json:"node_id"`
		ThreadID string `json:"thread_id"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	run, err := e.CloneProject(r.Context(), SetupInput{ProjectID: r.PathValue("id"), NodeID: b.NodeID, ThreadID: b.ThreadID, Actor: event.ActorUser})
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run": run})
}

// recentDone is how long finished tasks stay on the overview.
const recentDone = 24 * time.Hour

// overview is everything the workbench's first screen draws, in one call:
// projects with their machines, machines with their load, personas, the
// open and recently finished tasks, and the active runs.
func (e *Engine) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := e.store()
	projects, err := st.Projects(ctx)
	if err != nil {
		internal(w, err)
		return
	}
	nodes, _ := st.Nodes(ctx)
	for i := range nodes {
		nodes[i].Online = e.Nodes.Online(nodes[i].ID)
		nodes[i].Admin = e.Nodes.AdminState(nodes[i].ID)
	}
	personas, _ := st.Personas(ctx)
	open, _ := st.Tasks(ctx, store.TaskFilter{Statuses: openStatuses, Limit: 1000})
	since := time.Now().Add(-recentDone).UnixMilli()
	closed, _ := st.Tasks(ctx, store.TaskFilter{Statuses: []string{event.TaskDone, event.TaskFailed}, Limit: 1000})
	tasks := append([]store.Task{}, open...)
	for _, t := range closed {
		if t.Status == event.TaskFailed {
			continue // failed tasks are open (openStatuses)
		}
		if t.UpdatedAt >= since {
			tasks = append(tasks, t)
		}
	}
	runs, _ := st.Runs(ctx, store.RunFilter{Statuses: []string{event.RunStatusQueued, event.RunStatusRunning}, Limit: 1000})
	pending, _ := st.Approvals(ctx, "pending", "")
	repos, _ := st.RepoScans(ctx)
	unlinked := []store.RepoScan{}
	for _, rp := range repos {
		if rp.ProjectID == "" {
			unlinked = append(unlinked, rp)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projects": projects, "nodes": nodes, "personas": personas, "tasks": tasks, "runs": runs,
		"approvals": pending, "discovered": unlinked,
	})
}

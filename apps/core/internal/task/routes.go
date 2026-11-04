package task

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	projectkey "github.com/XiaoXianThis/Sous/apps/core/internal/project"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

// Routes registers the task, project and inbox endpoints.
func (e *Engine) Routes(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/tasks", e.listTasks)
	protect("POST /api/v1/tasks", e.createTask)
	protect("GET /api/v1/tasks/{id}", e.getTask)
	protect("PATCH /api/v1/tasks/{id}", e.patchTask)
	protect("POST /api/v1/tasks/{id}/dispatch", e.dispatchTask)
	protect("POST /api/v1/tasks/{id}/cancel", e.cancelTask)

	protect("GET /api/v1/projects", e.listProjects)
	protect("POST /api/v1/projects", e.putProject)
	protect("PATCH /api/v1/projects/{id}", e.putProject)
	protect("DELETE /api/v1/projects/{id}", e.deleteProject)
	protect("POST /api/v1/projects/{id}/archive", e.archiveProject(true))
	protect("POST /api/v1/projects/{id}/activate", e.archiveProject(false))

	protect("GET /api/v1/inbox", e.inbox)
	e.workbenchRoutes(protect)
}

func (e *Engine) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.TaskFilter{ProjectID: q.Get("project_id"), ThreadID: q.Get("thread_id"), ParentID: q.Get("parent_id")}
	if s := q.Get("status"); s != "" {
		f.Statuses = strings.Split(s, ",")
	}
	ts, err := e.store().Tasks(r.Context(), f)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": ts})
}

type taskBody struct {
	Title          *string   `json:"title"`
	Detail         *string   `json:"detail"`
	Kind           string    `json:"kind"`
	Priority       *int      `json:"priority"`
	ProjectID      *string   `json:"project_id"`
	AgentPref      *string   `json:"agent_pref"`
	NodePref       *string   `json:"node_pref"`
	PermissionMode *string   `json:"permission_mode"`
	ParentID       string    `json:"parent_id"`
	ThreadID       string    `json:"thread_id"`
	DependsOn      *[]string `json:"depends_on"`
	Status         *string   `json:"status"`
	Reason         string    `json:"reason"`
	Dismissed      *bool     `json:"dismissed"`
	Dispatch       bool      `json:"dispatch"`
	PersonaID      *string   `json:"persona_id"`
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func (e *Engine) createTask(w http.ResponseWriter, r *http.Request) {
	var b taskBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	t, err := e.Create(r.Context(), Input{
		ThreadID: b.ThreadID, ParentID: b.ParentID, Kind: b.Kind, Title: deref(b.Title), Detail: deref(b.Detail),
		Priority: deref(b.Priority), ProjectID: deref(b.ProjectID), Agent: deref(b.AgentPref), Node: deref(b.NodePref),
		Mode: deref(b.PermissionMode), DependsOn: deref(b.DependsOn), Dispatch: b.Dispatch, Actor: event.ActorUser,
		Persona: deref(b.PersonaID),
	})
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// getTask returns the task, its runs, its subtasks and its approvals.
func (e *Engine) getTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := e.store().Task(r.Context(), id)
	if err != nil {
		taskError(w, err)
		return
	}
	runs, _ := e.store().Runs(r.Context(), store.RunFilter{TaskID: id})
	subs, _ := e.store().Tasks(r.Context(), store.TaskFilter{ParentID: id})
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "runs": runs, "subtasks": subs})
}

func (e *Engine) patchTask(w http.ResponseWriter, r *http.Request) {
	var b taskBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	id := r.PathValue("id")
	f := event.TaskFields{Title: b.Title, Detail: b.Detail, Priority: b.Priority, ProjectID: b.ProjectID, AgentPref: b.AgentPref,
		NodePref: b.NodePref, PermissionMode: b.PermissionMode, Dismissed: b.Dismissed, PersonaID: b.PersonaID}
	if b.DependsOn != nil {
		f.DependsOn, f.SetDeps = *b.DependsOn, true
	}
	t, err := e.Update(r.Context(), id, f, event.ActorUser)
	if err == nil && b.Status != nil {
		t, err = e.SetStatus(r.Context(), id, *b.Status, b.Reason, event.ActorUser)
	}
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (e *Engine) dispatchTask(w http.ResponseWriter, r *http.Request) {
	var b struct {
		NodeID string `json:"node_id"`
		Agent  string `json:"agent"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	t, err := e.Dispatch(r.Context(), r.PathValue("id"), b.NodeID, b.Agent, event.ActorUser)
	if err != nil {
		taskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (e *Engine) cancelTask(w http.ResponseWriter, r *http.Request) {
	var b struct{ Reason string }
	json.NewDecoder(r.Body).Decode(&b)
	if err := e.Cancel(r.Context(), r.PathValue("id"), b.Reason, event.ActorUser); err != nil {
		taskError(w, err)
		return
	}
	t, _ := e.store().Task(r.Context(), r.PathValue("id"))
	writeJSON(w, http.StatusOK, t)
}

func (e *Engine) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := e.store().Projects(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
}

// putProject creates (POST) or replaces (PATCH) a project with its machines.
func (e *Engine) putProject(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name           *string              `json:"name"`
		RepoURL        *string              `json:"repo_url"`
		DefaultMode    *string              `json:"default_mode"`
		DefaultAgent   *string              `json:"default_agent"`
		DefaultPersona *string              `json:"default_persona"`
		Notes          *string              `json:"notes"`
		Layout         *string              `json:"layout"`
		Nodes          *[]event.ProjectNode `json:"nodes"`
		Deploy         *json.RawMessage     `json:"deploy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	in := ProjectInput{ID: r.PathValue("id"), Name: b.Name, RepoURL: b.RepoURL, DefaultMode: b.DefaultMode, DefaultAgent: b.DefaultAgent,
		DefaultPersona: b.DefaultPersona, Notes: b.Notes, Layout: b.Layout, Nodes: b.Nodes}
	if r.Method == http.MethodPost {
		if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
			writeError(w, http.StatusBadRequest, "invalid", "项目需要名字")
			return
		}
	}
	if b.Deploy != nil {
		var d *event.Deploy
		if string(*b.Deploy) != "null" {
			d = &event.Deploy{}
			if err := json.Unmarshal(*b.Deploy, d); err != nil {
				writeError(w, http.StatusBadRequest, "bad_json", err.Error())
				return
			}
		}
		in.Deploy = &d
	}
	p, err := e.PutProject(r.Context(), in, event.ActorUser)
	if err != nil {
		taskError(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, p)
}

// archiveProject archives a project or makes it active again.
func (e *Engine) archiveProject(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := e.ArchiveProject(r.Context(), r.PathValue("id"), archived, event.ActorUser)
		if err != nil {
			taskError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func (e *Engine) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == projectkey.MiscID {
		writeError(w, http.StatusConflict, "builtin", "内置的“杂项”项目不能删除")
		return
	}
	if _, err := e.store().Project(r.Context(), id); err != nil {
		taskError(w, err)
		return
	}
	open, _ := e.store().Tasks(r.Context(), store.TaskFilter{ProjectID: id, Statuses: openStatuses})
	if len(open) > 0 {
		writeError(w, http.StatusConflict, "in_use", "这个项目还有未关闭的任务")
		return
	}
	if _, err := e.Log.Append(r.Context(), event.New(event.ProjectDeleted, event.ActorUser, "", event.ProjectPayload{ProjectID: id})); err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// inbox lists what needs the user (design §13): pending approvals, tasks
// that are blocked, failed or waiting for a machine, and pull requests to
// review that the user has not dismissed.
func (e *Engine) inbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	approvals, err := e.store().Approvals(ctx, "pending", "")
	if err != nil {
		internal(w, err)
		return
	}
	attention, _ := e.store().Tasks(ctx, store.TaskFilter{Statuses: []string{event.TaskBlocked, event.TaskFailed, event.TaskQueued}})
	var tasks []store.Task
	for _, t := range attention {
		if t.Dismissed || t.Status == event.TaskQueued && !strings.HasPrefix(t.StatusReason, "等待机器") {
			continue
		}
		tasks = append(tasks, t)
	}
	running, _ := e.store().Tasks(ctx, store.TaskFilter{Statuses: []string{event.TaskRunning, event.TaskWaitingApproval}})
	for _, t := range running {
		if strings.HasPrefix(t.StatusReason, PausedPrefix) {
			tasks = append(tasks, t)
		}
	}
	done, _ := e.store().Tasks(ctx, store.TaskFilter{Statuses: []string{event.TaskDone}})
	for _, t := range done {
		if t.PRURL != "" && !t.Dismissed {
			tasks = append(tasks, t)
		}
	}
	if tasks == nil {
		tasks = []store.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals, "tasks": tasks})
}

func taskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "不存在")
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		internal(w, err)
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

func internal(w http.ResponseWriter, err error) {
	slog.Error("task: internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
}

// Modes lists the permission modes, for settings.
var Modes = []string{proto.ModeDefault, proto.ModeAuto, proto.ModeOpen}

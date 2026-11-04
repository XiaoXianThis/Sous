package task

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func (e *Engine) workbenchRoutes(protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/personas", e.listPersonas)
	protect("POST /api/v1/personas", e.putPersona)
	protect("PATCH /api/v1/personas/{id}", e.putPersona)
	protect("DELETE /api/v1/personas/{id}", e.deletePersona)

	protect("POST /api/v1/projects/new", e.newProject)
	protect("POST /api/v1/projects/{id}/clone", e.cloneProject)

	protect("GET /api/v1/overview", e.overview)
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

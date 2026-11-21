package hub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// AgentFactory is a trusted, in-process assembly hook, not a plugin interface
// exposed to agents. An external agent must receive scoped tools, never these
// dependencies or a Hub database handle.
type AgentFactory func(AgentDeps) (AgentModule, error)

type AgentDeps struct {
	Config *config.Hub
	Log    *eventlog.Log
	Tools  *tool.Registry
	Tasks  *task.Engine
	Nodes  *nodehub.Hub
	Auth   *auth.Auth
}

// AgentModule is optional. Without its advisors the task engine uses manual
// approval/review and the inspector runs its deterministic rules.
type AgentModule struct {
	Name        string
	Identity    string                      // safe fingerprint of the configuration pinned to this instance
	Probe       func(context.Context) error // validation with no production data, before the detach barrier
	Start       func(context.Context)
	Wait        func()      // waits for Start's work after its context is cancelled
	PauseIfIdle func() bool // starts an idle-only detach barrier
	Resume      func()      // reopens the barrier and recovers pending work
	Active      func(threadID string) bool
	Available   func(context.Context) bool
	RoleRefs    func(context.Context) map[string]string
	Routes      []httpapi.Routes
	Reviewer    task.Model
	Approver    task.Model
	Inspector   task.Model
}

func (m AgentModule) roleRefs(ctx context.Context) map[string]string {
	if m.RoleRefs != nil {
		return m.RoleRefs(ctx)
	}
	return map[string]string{}
}

func (h *Hub) capabilityRoutes(_ *http.ServeMux, protect func(string, http.HandlerFunc)) {
	protect("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		status := h.module.status(r.Context())
		if h.bindings != nil {
			status = h.bindings.status(r.Context())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"supervisor": status, "node_pairing": !h.Cfg.DirectWorkspace(), "hosted_workspace": h.Cfg.HostedWorkspace, "external_mcp": h.Cfg.HostedWorkspace && len(h.Cfg.ExternalMCPClients) > 0, "cloud_work": h.CloudWork != nil, "local_sandbox_dispatch": h.SandboxDispatch != nil,
		})
	})
}

func (m AgentModule) status(ctx context.Context) bindingStatus {
	v := bindingStatus{Module: m.Name, State: "unbound"}
	if m.Name != "" {
		v.State = "configured"
		if m.Available != nil && !m.Available(ctx) {
			v.State = "unconfigured"
		}
	}
	return v
}

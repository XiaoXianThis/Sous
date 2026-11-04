package event

import "github.com/XiaoXianThis/Sous/apps/core/internal/proto"

// Milestone M5 (work/specs/m5-workbench): personas, the repositories a
// machine found in its workspaces, machine roles and project setup.
const (
	PersonaPut     Type = "persona.put"
	PersonaDeleted Type = "persona.deleted"

	// NodeRepos replaces what a machine reported about its workspaces.
	NodeRepos Type = "node.repos"
	// NodeRole records a machine's role (dev / server).
	NodeRole Type = "node.role"
)

// Task and run kinds added in M5.
const (
	TaskKindOps = "ops" // in place, in a project's directory on a server
	RunKindOps  = "ops"
)

// Run purposes added in M5: the node clones or creates a project itself.
const (
	PurposeClone  = "clone"
	PurposeCreate = "create"
)

// PersonaPayload is a persona (a "station" in the brigade): who does a task.
type PersonaPayload struct {
	PersonaID   string            `json:"persona_id"`
	Name        string            `json:"name,omitempty"`
	Glyph       string            `json:"glyph,omitempty"` // one character for the avatar
	Color       string            `json:"color,omitempty"` // purple, teal, coral, pink, blue, green, amber, gray
	Agent       string            `json:"agent,omitempty"` // empty: the project's or the global default
	Options     map[string]string `json:"options,omitempty"`
	Brief       string            `json:"brief,omitempty"` // put before the task in the agent's prompt
	DefaultMode string            `json:"default_mode,omitempty"`
	Kinds       []string          `json:"kinds,omitempty"` // task kinds it suits
	Builtin     bool              `json:"builtin,omitempty"`
}

type NodeReposPayload struct {
	NodeID string `json:"node_id"`
	proto.NodeRepos
}

type NodeRolePayload struct {
	NodeID string `json:"node_id"`
	Role   string `json:"role"`
}

// MessageContext is what the user was looking at when they wrote.
type MessageContext struct {
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
}

// ProjectRepoOp records a sync or WIP save of a project's checkout that
// the user asked for from the page (multi-machine-sync).
const ProjectRepoOp Type = "project.repo_op"

type ProjectRepoOpPayload struct {
	ProjectID string `json:"project_id"`
	NodeID    string `json:"node_id"`
	Path      string `json:"path"`
	Op        string `json:"op"` // repo.sync / repo.wip
	Outcome   string `json:"outcome"`
	Message   string `json:"message"`
	Branch    string `json:"branch,omitempty"`
}

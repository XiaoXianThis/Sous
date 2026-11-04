package event

import "github.com/XiaoXianThis/Sous/apps/core/internal/proto"

// Task and project events (milestone M3).
const (
	TaskCreated  Type = "task.created"
	TaskUpdated  Type = "task.updated"  // fields the user or the supervisor changed
	TaskStatus   Type = "task.status"   // a status transition
	TaskReviewed Type = "task.reviewed" // the reviewer's verdict on a run

	ProjectPut     Type = "project.put"
	ProjectDeleted Type = "project.deleted"
	// ProjectArchived archives a project or makes it active again. It is its
	// own event so that project.put never touches the archived state.
	ProjectArchived Type = "project.archived"

	NotifySent Type = "notify.sent"
)

// Task kinds.
const (
	TaskKindCode    = "code"    // in a git worktree of a project's repository
	TaskKindGeneral = "general" // in a scratch directory
)

// Task statuses (requirements R2.2). Queued means the task wants to run but
// waits for a machine or for the tasks it depends on.
const (
	TaskInbox           = "inbox"
	TaskPlanned         = "planned"
	TaskQueued          = "queued"
	TaskRunning         = "running"
	TaskWaitingApproval = "waiting_approval"
	TaskBlocked         = "blocked"
	TaskReview          = "review"
	TaskDone            = "done"
	TaskFailed          = "failed"
	TaskCancelled       = "cancelled"
)

// Message kind of a task report the engine posts in the task's thread.
const KindReport = "report"

// Run purposes: what a run does for its task.
const (
	PurposeWork    = "work"    // an agent does the task
	PurposeRebase  = "rebase"  // an agent rebases the task's branch after a conflict
	PurposeDeliver = "deliver" // the node pushes the branch and opens a pull request
)

// TaskFields are the editable fields of a task. In task.updated only the
// fields that changed are set; pointers tell "unset" from "cleared".
type TaskFields struct {
	Title          *string  `json:"title,omitempty"`
	Detail         *string  `json:"detail,omitempty"`
	Priority       *int     `json:"priority,omitempty"`
	ProjectID      *string  `json:"project_id,omitempty"`
	AgentPref      *string  `json:"agent_pref,omitempty"`
	NodePref       *string  `json:"node_pref,omitempty"`
	PermissionMode *string  `json:"permission_mode,omitempty"`
	PersonaID      *string  `json:"persona_id,omitempty"`
	DependsOn      []string `json:"depends_on,omitempty"`
	SetDeps        bool     `json:"set_deps,omitempty"`  // DependsOn replaces the list, even when empty
	Dismissed      *bool    `json:"dismissed,omitempty"` // the user has seen the result
}

type TaskCreatedPayload struct {
	ParentID       string   `json:"parent_id,omitempty"`
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	Detail         string   `json:"detail,omitempty"`
	Priority       int      `json:"priority,omitempty"`
	ProjectID      string   `json:"project_id,omitempty"`
	AgentPref      string   `json:"agent_pref,omitempty"`
	NodePref       string   `json:"node_pref,omitempty"`
	PermissionMode string   `json:"permission_mode,omitempty"` // empty: the project's or the global default
	PersonaID      string   `json:"persona_id,omitempty"`
	DependsOn      []string `json:"depends_on,omitempty"`
	Status         string   `json:"status"`
}

type TaskUpdatedPayload struct {
	TaskFields
}

type TaskStatusPayload struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason,omitempty"`
	// Set when a delivery opened a pull request.
	PRURL string `json:"pr_url,omitempty"`
	// Waiting says what a queued task waits for.
	Waiting string `json:"waiting,omitempty"`
	// Notify asks for a push notification for this change: set when a task
	// starts waiting for a machine, so the user can switch one on.
	Notify bool `json:"notify,omitempty"`
	// Next is the purpose of the run a queued task starts once it can:
	// work (default), rebase or deliver.
	Next string `json:"next,omitempty"`
}

// What a queued task waits for.
const (
	WaitingMachine = "machine"
	WaitingDeps    = "deps"
)

type TaskReviewedPayload struct {
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}

type ProjectNode struct {
	NodeID   string `json:"node_id"`
	Path     string `json:"path"`
	Priority int    `json:"priority"` // lower first
	// Source is "scan" for a place found in the machine's workspaces, else
	// "manual" (M5). Scan places come back on the next scan.
	Source string `json:"source,omitempty"`
	// Git is where the checkout stands, from the machine's last scan.
	// Read only: filled in when projects are read, never stored in events.
	Git *proto.GitState `json:"git,omitempty"`
}

// Deploy is where and how a project is deployed (M5).
type Deploy struct {
	NodeID      string `json:"node_id"`
	Path        string `json:"path"`
	Command     string `json:"command"`                 // the deploy script or command
	Health      string `json:"health,omitempty"`        // a health check command
	AutoOnMerge bool   `json:"auto_on_merge,omitempty"` // deploy when a PR sous opened is merged
}

type ProjectPayload struct {
	ProjectID    string        `json:"project_id"`
	Name         string        `json:"name,omitempty"`
	RepoURL      string        `json:"repo_url,omitempty"`
	DefaultMode  string        `json:"default_mode,omitempty"`
	DefaultAgent string        `json:"default_agent,omitempty"`
	Nodes        []ProjectNode `json:"nodes,omitempty"`
	// M5
	DefaultPersona string  `json:"default_persona,omitempty"`
	Notes          string  `json:"notes,omitempty"`
	Layout         string  `json:"layout,omitempty"` // wokspace / empty
	Deploy         *Deploy `json:"deploy,omitempty"`
}

// ProjectArchivedPayload archives a project (Archived) or activates it.
type ProjectArchivedPayload struct {
	ProjectID string `json:"project_id"`
	Archived  bool   `json:"archived"`
}

// NotifySentPayload records a push notification sent to the user's devices.
type NotifySentPayload struct {
	Kind  string `json:"kind"` // approval / task_done / task_failed / task_blocked / report / inspector
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Sent  int    `json:"sent"`
}

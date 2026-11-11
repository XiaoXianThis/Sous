package event

import "github.com/XiaoXianThis/Sous/apps/core/internal/proto"

// Task and project events (milestone M3).
const (
	TaskCreated  Type = "task.created"
	TaskUpdated  Type = "task.updated"  // fields the user or the supervisor changed
	TaskStatus   Type = "task.status"   // a status transition
	TaskReviewed Type = "task.reviewed" // the reviewer's verdict on a run
	// TaskGit records what a git operation did to a task's branch: merged,
	// pushed, a pull request, discarded (docs/decisions/0022).
	TaskGit Type = "task.git"

	ProjectPut     Type = "project.put"
	ProjectDeleted Type = "project.deleted"
	// ProjectArchived archives a project or makes it active again. It is its
	// own event so that project.put never touches the archived state.
	ProjectArchived Type = "project.archived"
	// ProjectDeployed records the commit of the deploy branch that is now
	// deployed, or the baseline when automatic deploys were switched on.
	ProjectDeployed Type = "project.deployed"

	NotifySent Type = "notify.sent"
)

// Task kinds. Since decision 0022 a kind is only a label (display, which
// persona suits it); where a task works and how it is reviewed are its own
// attributes, chosen by the supervisor.
const (
	TaskKindCode    = "code"
	TaskKindGeneral = "general"
)

// Where a task works (TaskCreatedPayload.Place).
const (
	PlaceWorktree = "worktree" // a git worktree on its own branch
	PlaceInPlace  = "in_place" // the project's directory itself, snapshotted first
	PlaceScratch  = "scratch"  // a scratch directory
)

// How a task's work is reviewed (TaskCreatedPayload.Review).
const (
	ReviewNone  = "none"  // no review: the agent's summary is the report
	ReviewModel = "model" // the reviewer model reads the diff
	ReviewAgent = "agent" // a reviewing agent checks it in the workdir, read only plus tests
	ReviewUser  = "user"  // the user decides
)

// What happens when a review fails (TaskCreatedPayload.OnFail).
const (
	OnFailRework = "rework" // run the task again with the reviewer's reason
	OnFailAsk    = "ask"    // block the task and let the supervisor or the user decide
)

// Where the start of a task branch comes from (BaseFrom).
const (
	BaseLocal  = "local"  // the local branch, with commits not pushed yet
	BaseRemote = "remote" // origin/<branch> after a fetch
)

// Git states of a task's branch (TaskGitPayload.State).
const (
	GitUnmerged  = "unmerged"
	GitMerged    = "merged"
	GitPR        = "pr"
	GitDiscarded = "discarded"
	GitRestored  = "restored" // an in-place task's changes were undone
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

// KindDM is what the user says to an agent directly, and the agent's answer
// (decision 0028). It does not start a topic and is not addressed to the supervisor.
const KindDM = "dm"

// Run purposes: what a run does.
const (
	PurposeWork = "work" // an agent does the task
	// Rebase and deliver are no longer started (decision 0022); old runs
	// in the log keep them.
	PurposeRebase  = "rebase"
	PurposeDeliver = "deliver"
	// PurposeReview: a reviewing agent checks a task's work, read only.
	PurposeReview = "review"
	// PurposeGit: the node runs one git operation with fixed commands.
	PurposeGit = "git"
	// PurposeConsult: a project's read-only consultant session.
	PurposeConsult = "consult"
	// PurposeChat: the user talks to a task's agent directly (decision 0028).
	PurposeChat = "chat"
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
	// Decision 0022: where the task works, how it is reviewed, where its
	// branch starts.
	Place      *string `json:"place,omitempty"`
	Review     *string `json:"review,omitempty"`
	OnFail     *string `json:"on_fail,omitempty"`
	BaseBranch *string `json:"base_branch,omitempty"`
	BaseFrom   *string `json:"base_from,omitempty"`
	// Options are the session settings the task asks for ("model",
	// "effort"), over its preset's; an empty map clears them.
	Options *map[string]string `json:"options,omitempty"`
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
	// Decision 0022.
	Place      string   `json:"place,omitempty"`
	Review     string   `json:"review,omitempty"`
	OnFail     string   `json:"on_fail,omitempty"`
	BaseBranch string   `json:"base_branch,omitempty"`
	BaseFrom   string   `json:"base_from,omitempty"`
	Handoff    *Handoff `json:"handoff,omitempty"`
	// Options are the session settings the task asks for ("model",
	// "effort"), over its preset's.
	Options map[string]string `json:"options,omitempty"`
	// DeployCommit is the deploy branch's commit an automatic deploy is for.
	DeployCommit string `json:"deploy_commit,omitempty"`
	// TopicID is the conversation topic the task belongs to (chat-topics).
	TopicID string `json:"topic_id,omitempty"`
}

// Handoff carries a project consultant's session into a task that grew out
// of the discussion, so the agent that did the reading keeps going.
type Handoff struct {
	NodeID    string `json:"node_id"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id,omitempty"`
	// Note is what was discussed, for an agent that cannot load the session.
	Note string `json:"note,omitempty"`
}

// TaskGitPayload records a git operation on a task's branch.
type TaskGitPayload struct {
	State string `json:"state"` // unmerged / merged / pr / discarded / restored
	Note  string `json:"note,omitempty"`
	PRURL string `json:"pr_url,omitempty"`
	RunID string `json:"run_id,omitempty"`
}

// ProjectDeployedPayload records what is deployed from the deploy branch.
type ProjectDeployedPayload struct {
	ProjectID string `json:"project_id"`
	Commit    string `json:"commit"`
	// Baseline marks the commit found when automatic deploys started: it
	// was not deployed by sous, only remembered.
	Baseline bool `json:"baseline,omitempty"`
}

type TaskUpdatedPayload struct {
	TaskFields
	// TopicID puts a task that had no topic into one (chat-topics): set by
	// the engine with its first report, never by a request.
	TopicID *string `json:"topic_id,omitempty"`
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
	NodeID  string `json:"node_id"`
	Path    string `json:"path"`
	Command string `json:"command"`          // the deploy script or command
	Health  string `json:"health,omitempty"` // a health check command
	// Branch is the deploy branch: new commits on origin/<branch> are what
	// gets deployed. No default (decision 0022).
	Branch string `json:"branch,omitempty"`
	// AutoOnMerge deploys every new commit of Branch automatically. The
	// name is kept from when it meant "a pull request sous opened was
	// merged"; without a Branch it does nothing.
	AutoOnMerge bool `json:"auto_on_merge,omitempty"`
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

package event

import (
	"encoding/json"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Payloads of node, run and approval events.

type NodePayload struct {
	NodeID   string          `json:"node_id"`
	Name     string          `json:"name,omitempty"`
	OS       string          `json:"os,omitempty"`
	Arch     string          `json:"arch,omitempty"`
	Hostname string          `json:"hostname,omitempty"`
	Version  string          `json:"version,omitempty"`
	Agents   json.RawMessage `json:"agents,omitempty"` // node.inventory: []proto.AgentInfo
	// Concurrency is how many runs the node executes at once (node.inventory).
	Concurrency int `json:"concurrency,omitempty"`
	// M5 (node.inventory): role, workspaces and the wok version.
	Role       string   `json:"role,omitempty"`
	Workspaces []string `json:"workspaces,omitempty"`
	Wok        *string  `json:"wok,omitempty"`
}

// Run kinds.
const (
	RunKindCode    = "code"    // in a git worktree of a repository
	RunKindGeneral = "general" // in a scratch directory
)

// Run statuses.
const (
	RunStatusQueued      = "queued"
	RunStatusRunning     = "running"
	RunStatusSucceeded   = "succeeded"
	RunStatusFailed      = "failed"
	RunStatusCancelled   = "cancelled"
	RunStatusInterrupted = "interrupted"
)

type RunQueuedPayload struct {
	NodeID         string `json:"node_id"`
	Agent          string `json:"agent"`
	Kind           string `json:"kind"`
	RepoPath       string `json:"repo_path,omitempty"`
	Prompt         string `json:"prompt"`
	PermissionMode string `json:"permission_mode"`
	// Task runs only.
	Purpose    string `json:"purpose,omitempty"`     // work / rebase / deliver
	Branch     string `json:"branch,omitempty"`      // the task's branch, reused by every run
	BaseBranch string `json:"base_branch,omitempty"` // the branch a pull request targets
	Title      string `json:"title,omitempty"`       // the task title, for commits and pull requests
	Force      bool   `json:"force,omitempty"`       // deliver: push with --force-with-lease after a rebase
	// RecoveryNote comes first in the prompt of a run that picks up after
	// an interrupted one (design §9).
	RecoveryNote string `json:"recovery_note,omitempty"`
	// M5: the persona's session options and name, and what a setup run does.
	Options map[string]string `json:"options,omitempty"`
	Persona string            `json:"persona,omitempty"`
	Setup   *proto.Setup      `json:"setup,omitempty"`
	// Decision 0022: where a new task branch starts, the git operation of a
	// git run, the project of a consult run, and an agent session to load.
	BaseFrom      string       `json:"base_from,omitempty"`
	Git           *proto.GitOp `json:"git,omitempty"`
	ProjectID     string       `json:"project_id,omitempty"`
	ResumeSession string       `json:"resume_session,omitempty"`
}

type RunStartedPayload struct {
	Workdir    string `json:"workdir"`
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
}

// RunTurnPayload is one finished turn of a long-lived run (a consultant):
// its whole answer and the agent's session id.
type RunTurnPayload struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
}

// RunUpdatePayload mirrors proto.RunUpdate.
type RunUpdatePayload struct {
	Kind       string          `json:"kind"` // tool / plan / text / thought / status / permission
	Text       string          `json:"text,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Title      string          `json:"title,omitempty"`
	Status     string          `json:"status,omitempty"`
	ToolKind   string          `json:"tool_kind,omitempty"`
	Plan       json.RawMessage `json:"plan,omitempty"`
	// permission: what the node's policy decided on its own.
	Category string `json:"category,omitempty"`
	Decision string `json:"decision,omitempty"` // allow / deny
}

type RunEndedPayload struct {
	Status     string `json:"status"`
	StopReason string `json:"stop_reason,omitempty"`
	Summary    string `json:"summary,omitempty"`
	DiffStat   string `json:"diffstat,omitempty"`
	Error      string `json:"error,omitempty"`
	// Deliver runs: the pull request, and whether it conflicts with its base.
	PRURL    string `json:"pr_url,omitempty"`
	Conflict bool   `json:"conflict,omitempty"`
	// Git runs: what the operation did. Consult runs: the session to load
	// next time.
	Git       *proto.GitResult `json:"git,omitempty"`
	SessionID string           `json:"session_id,omitempty"`
	// See proto.RunEnded.
	Outcome   string   `json:"outcome,omitempty"`
	ErrorKind string   `json:"error_kind,omitempty"`
	Refused   []string `json:"refused,omitempty"`
}

type ApprovalOption struct {
	OptionID string `json:"option_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once, reject_always
}

type ApprovalRequestedPayload struct {
	ApprovalID string           `json:"approval_id"`
	Title      string           `json:"title"`
	ToolKind   string           `json:"tool_kind,omitempty"`
	Detail     string           `json:"detail,omitempty"`
	Options    []ApprovalOption `json:"options"`
	// Category is the node policy's class of the action (read, exec-other,
	// git-remote, ...). Review says who decides: "user", or "ai" for the
	// approver agent, which hands what it is unsure of to the user.
	Category string `json:"category,omitempty"`
	Review   string `json:"review,omitempty"`
}

type ApprovalDecidedPayload struct {
	ApprovalID string `json:"approval_id"`
	OptionID   string `json:"option_id"`  // empty: cancelled
	DecidedBy  string `json:"decided_by"` // user / approver
	Note       string `json:"note,omitempty"`
}

// ApprovalEscalatedPayload: the approver agent handed an approval to the user.
type ApprovalEscalatedPayload struct {
	ApprovalID string `json:"approval_id"`
	Note       string `json:"note"`
}

package event

import "encoding/json"

// Payloads of node, run and approval events.

type NodePayload struct {
	NodeID   string          `json:"node_id"`
	Name     string          `json:"name,omitempty"`
	OS       string          `json:"os,omitempty"`
	Arch     string          `json:"arch,omitempty"`
	Hostname string          `json:"hostname,omitempty"`
	Version  string          `json:"version,omitempty"`
	Agents   json.RawMessage `json:"agents,omitempty"` // node.inventory: []proto.AgentInfo
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
}

type RunStartedPayload struct {
	Workdir string `json:"workdir"`
	Branch  string `json:"branch,omitempty"`
}

// RunUpdatePayload mirrors proto.RunUpdate.
type RunUpdatePayload struct {
	Kind       string          `json:"kind"` // tool / plan / text / thought / status
	Text       string          `json:"text,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Title      string          `json:"title,omitempty"`
	Status     string          `json:"status,omitempty"`
	ToolKind   string          `json:"tool_kind,omitempty"`
	Plan       json.RawMessage `json:"plan,omitempty"`
}

type RunEndedPayload struct {
	Status     string `json:"status"`
	StopReason string `json:"stop_reason,omitempty"`
	Summary    string `json:"summary,omitempty"`
	DiffStat   string `json:"diffstat,omitempty"`
	Error      string `json:"error,omitempty"`
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
}

type ApprovalDecidedPayload struct {
	ApprovalID string `json:"approval_id"`
	OptionID   string `json:"option_id"` // empty: cancelled
	DecidedBy  string `json:"decided_by"`
	Note       string `json:"note,omitempty"`
}

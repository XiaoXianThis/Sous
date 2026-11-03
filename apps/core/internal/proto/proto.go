// Package proto holds the Hub ↔ Node protocol (docs/api/core-hub-node.md).
// Both binaries use these types. Frames are JSON objects over a WebSocket
// that the node opens to the Hub's /node endpoint.
package proto

import (
	"encoding/json"
	"fmt"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
)

// Frame types.
const (
	THello     = "hello"
	TWelcome   = "welcome"
	TInventory = "inventory"
	TAck       = "ack"

	TRunStart   = "run.start"
	TRunMessage = "run.message"
	TRunCancel  = "run.cancel"
	TRunStarted = "run.started"
	TRunUpdate  = "run.update"
	TRunOutput  = "run.output" // not acknowledged: the Hub only relays it
	TRunDiff    = "run.diff"
	TRunEnded   = "run.ended"

	TApprovalRequest = "approval.request"
	TApprovalDecide  = "approval.decide"

	TWorkspaceRemove = "workspace.remove"
)

// Frame is one message. Frames from the node that must not be lost carry a
// seq; the Hub acknowledges each once it is stored, and the node resends
// unacknowledged frames after a reconnect. The Hub ignores frame ids it has
// already seen.
type Frame struct {
	T    string          `json:"t"`
	ID   string          `json:"id"`
	Seq  int64           `json:"seq,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

// New builds a frame with a fresh id.
func New(t string, body any) Frame {
	raw, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("proto: marshal %s: %v", t, err))
	}
	return Frame{T: t, ID: ids.New(), Body: raw}
}

func (f Frame) Decode(v any) error { return json.Unmarshal(f.Body, v) }

type Hello struct {
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
	// LastAckedSeq lets the Hub know which frames the node will resend.
	LastAckedSeq int64 `json:"last_acked_seq"`
}

type Welcome struct {
	NodeID string `json:"node_id"`
	// ResumeRuns are the runs the Hub believes are still running on this
	// node. The node ends each one it no longer has as interrupted.
	ResumeRuns []string `json:"resume_runs"`
}

// AgentInfo describes one coding agent on a node.
type AgentInfo struct {
	Name      string `json:"name"`  // claude, codex, cursor, opencode, kiro
	Title     string `json:"title"` // display name
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Transport string `json:"transport"` // acp
	// LoggedIn is nil when the node cannot tell.
	LoggedIn *bool  `json:"logged_in,omitempty"`
	Note     string `json:"note,omitempty"`
}

type Inventory struct {
	Agents      []AgentInfo `json:"agents"`
	Concurrency int         `json:"concurrency"`
}

type Ack struct {
	Seq int64 `json:"seq"`
}

// Permission modes (docs/decisions/0005).
const (
	ModeDefault = "default" // ask before changes
	ModeAuto    = "auto"    // an AI approver decides, and hands what it is unsure of to the user
	ModeOpen    = "open"    // allow everything
)

type RunStart struct {
	RunID          string `json:"run_id"`
	Agent          string `json:"agent"`
	Kind           string `json:"kind"` // code / general
	RepoPath       string `json:"repo_path,omitempty"`
	Prompt         string `json:"prompt"`
	PermissionMode string `json:"permission_mode"`
	RecoveryNote   string `json:"recovery_note,omitempty"`
	// Task runs: every run of a task shares one worktree and branch.
	TaskID string `json:"task_id,omitempty"`
	// Purpose is work (default), rebase, or deliver: push the branch and
	// open a pull request, with no agent.
	Purpose    string `json:"purpose,omitempty"`
	Branch     string `json:"branch,omitempty"` // empty on a task's first run: the node names it
	BaseBranch string `json:"base_branch,omitempty"`
	Title      string `json:"title,omitempty"` // the task title: branch name, commit and PR title
	Force      bool   `json:"force,omitempty"` // deliver after a rebase: push --force-with-lease
}

// Run purposes.
const (
	PurposeWork    = "work"
	PurposeRebase  = "rebase"
	PurposeDeliver = "deliver"
)

type RunMessage struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

type RunCancel struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason,omitempty"`
}

type RunStarted struct {
	RunID      string `json:"run_id"`
	Workdir    string `json:"workdir"`
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// RunUpdate kinds.
const (
	UpdateTool    = "tool"    // a tool call started or changed
	UpdatePlan    = "plan"    // the agent's plan
	UpdateStatus  = "status"  // a short status line from sous itself
	UpdateText    = "text"    // agent message text (run.output)
	UpdateThought = "thought" // agent reasoning text (run.output)
	// UpdatePermission records what the node's policy decided on its own:
	// Title is the action, Category its class, Status allow or deny.
	UpdatePermission = "permission"
)

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type RunUpdate struct {
	RunID      string      `json:"run_id"`
	Kind       string      `json:"kind"`
	Text       string      `json:"text,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Title      string      `json:"title,omitempty"`
	Status     string      `json:"status,omitempty"`
	ToolKind   string      `json:"tool_kind,omitempty"`
	Plan       []PlanEntry `json:"plan,omitempty"`
	Category   string      `json:"category,omitempty"`
}

// RunDiff carries the run's diff in chunks; Final marks the last one.
type RunDiff struct {
	RunID  string `json:"run_id"`
	Offset int64  `json:"offset"`
	Chunk  string `json:"chunk"`
	Final  bool   `json:"final"`
}

// DiffChunkSize bounds one run.diff chunk.
const DiffChunkSize = 256 * 1024

type RunEnded struct {
	RunID      string `json:"run_id"`
	Status     string `json:"status"` // succeeded / failed / cancelled / interrupted
	StopReason string `json:"stop_reason,omitempty"`
	Summary    string `json:"summary,omitempty"`
	DiffStat   string `json:"diffstat,omitempty"`
	Error      string `json:"error,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`   // deliver
	Conflict   bool   `json:"conflict,omitempty"` // deliver: the PR conflicts with its base
}

type Option struct {
	OptionID string `json:"option_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type ApprovalRequest struct {
	ApprovalID string   `json:"approval_id"`
	RunID      string   `json:"run_id"`
	Title      string   `json:"title"`
	ToolKind   string   `json:"tool_kind,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	Options    []Option `json:"options"`
	// Category is the node policy's class of the action; Review is "user",
	// or "ai" when the Hub's approver agent may decide (auto mode).
	Category string `json:"category,omitempty"`
	Review   string `json:"review,omitempty"`
}

type ApprovalDecide struct {
	ApprovalID string `json:"approval_id"`
	RunID      string `json:"run_id"`
	OptionID   string `json:"option_id"` // empty: cancel the request
	Note       string `json:"note,omitempty"`
}

// WorkspaceRemove asks a node to delete a closed task's worktree. The
// branch stays.
type WorkspaceRemove struct {
	TaskID   string `json:"task_id"`
	RepoPath string `json:"repo_path"`
}

// PairRequest is the body of POST /api/v1/node/pair.
type PairRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
}

type PairResponse struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"`
}

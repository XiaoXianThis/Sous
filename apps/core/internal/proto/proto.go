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

	// M4
	TRunPause     = "run.pause"     // stop the agent's current turn and wait
	TRunResume    = "run.resume"    // continue a paused run
	TRunDelivered = "run.delivered" // follow-up messages reached the agent's session
	TNodeUpdate   = "node.update"   // download a release and restart
	// TNodeUpdateResult reports how an update went, before the restart.
	TNodeUpdateResult = "node.update_result"

	// TRunTurn reports one finished turn of a long-lived run (a project
	// consultant, decision 0022). Reliable.
	TRunTurn = "run.turn"
	TRunAsk  = "run.ask" // node → Hub: an agent asks the supervisor (0025)
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
	// Caps lists optional features the node supports (CapAdmin).
	Caps []string `json:"caps,omitempty"`
	// Admin is false when node.json turned remote administration off.
	Admin bool `json:"admin,omitempty"`
	// Addrs are the machine's own IP addresses (not loopback or
	// link-local): the Hub recognizes an ssh to a connected machine by them.
	Addrs []string `json:"addrs,omitempty"`
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
	// Decision 0024, from node.json: Disabled agents are not used on this
	// machine; Default is the machine's default agent; Custom marks an
	// agent added in custom_agents.
	Disabled bool `json:"disabled,omitempty"`
	Default  bool `json:"default,omitempty"`
	Custom   bool `json:"custom,omitempty"`

	// Remote administration (nodes with CapAdmin).
	// Install, Upgrade, Login and Logout are the commands sous would run;
	// empty when there is none on this system.
	Install string `json:"install,omitempty"`
	Upgrade string `json:"upgrade,omitempty"`
	Login   string `json:"login,omitempty"`
	Logout  string `json:"logout,omitempty"`
	// Missing lists commands the agent needs that are not on PATH (npx).
	Missing []string `json:"missing,omitempty"`
	// KeyEnv are the environment variables that can hold an API key;
	// EnvSet the ones set in node.json (names only).
	KeyEnv []string `json:"key_env,omitempty"`
	EnvSet []string `json:"env_set,omitempty"`
	// Options are the session settings the agent reported the last time a
	// session opened; Defaults what node.json sets them to.
	Options    []ConfigOption    `json:"options,omitempty"`
	OptionsAt  int64             `json:"options_at,omitempty"` // ms
	Defaults   map[string]string `json:"defaults,omitempty"`
	Configs    []AgentConfigFile `json:"configs,omitempty"`
	LaunchOver bool              `json:"launch_override,omitempty"` // node.json overrides how it starts
	// Job is the install or login running for the agent now, if any.
	Job *JobStarted `json:"job,omitempty"`
}

type Inventory struct {
	Agents      []AgentInfo `json:"agents"`
	Concurrency int         `json:"concurrency"`
	// M5: the machine's role (node.json), its workspaces, and the version
	// of wok on PATH (empty: not installed).
	Role       string   `json:"role,omitempty"`
	Workspaces []string `json:"workspaces,omitempty"`
	Wok        string   `json:"wok,omitempty"`
}

type Ack struct {
	Seq int64 `json:"seq"`
}

// Permission modes (docs/decisions/0005).
const (
	ModeDefault = "default" // ask before changes
	ModeAuto    = "auto"    // an AI approver decides, and hands what it is unsure of to the user
	ModeOpen    = "open"    // allow everything
	// Decision 0022: set by the node for consult and review runs, whatever
	// the Hub sends. ReadOnly allows reading only; Verify adds the safe
	// commands (tests, builds) and refuses everything else, without asking.
	ModeReadOnly = "readonly"
	ModeVerify   = "verify"
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
	// M5: Options are session config options the task's persona sets (they
	// win over node.json's defaults, never over what sous forces); Persona
	// names it, for status lines. Setup is for clone and create runs.
	Options map[string]string `json:"options,omitempty"`
	Persona string            `json:"persona,omitempty"`
	Setup   *Setup            `json:"setup,omitempty"`
	// Decision 0022. BaseFrom is where a new task branch starts: "local"
	// (the local BaseBranch, unpushed commits included) or "remote"
	// (origin/BaseBranch after a fetch). There is no default: a new branch
	// needs both. Git is the operation of a git run. ResumeSession is an
	// agent session to load instead of opening a new one (a consultant
	// that went idle, or a task that grew out of a discussion).
	BaseFrom      string `json:"base_from,omitempty"`
	Git           *GitOp `json:"git,omitempty"`
	ResumeSession string `json:"resume_session,omitempty"`
	// ContinuePrompt is sent instead of Prompt when ResumeSession loads: a
	// rework or retry that goes on in the task's earlier session (0025).
	ContinuePrompt string `json:"continue_prompt,omitempty"`
}

// Run purposes.
const (
	PurposeWork    = "work"
	PurposeRebase  = "rebase"
	PurposeDeliver = "deliver"
	// Decision 0022.
	PurposeReview  = "review"  // a reviewing agent checks a task's work: read only plus tests
	PurposeGit     = "git"     // one git operation, fixed commands, no agent
	PurposeConsult = "consult" // a project's read-only consultant, idle between questions
)

// RunAsk is a question a run's agent asks the supervisor (decision 0025,
// node → Hub). The answer comes back as a run.message.
type RunAsk struct {
	RunID    string `json:"run_id"`
	Seq      int    `json:"seq"`
	Question string `json:"question"`
}

// RunTurn is one finished turn of a long-lived run.
type RunTurn struct {
	RunID     string `json:"run_id"`
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
}

type RunMessage struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
	// MessageID lets the node drop a message it already has and report
	// which ones the agent read (run.delivered).
	MessageID string `json:"message_id,omitempty"`
}

// RunPause asks the node to stop the agent's current turn and hold the run
// until run.resume, a run.message (which resumes with that text) or run.cancel.
type RunPause struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason,omitempty"`
}

type RunResume struct {
	RunID string `json:"run_id"`
	Text  string `json:"text,omitempty"` // empty: "continue"
}

// RunDelivered reports follow-up messages that are now in the agent's session.
type RunDelivered struct {
	RunID      string   `json:"run_id"`
	MessageIDs []string `json:"message_ids"`
}

type NodeUpdateResult struct {
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NodeUpdate asks the node to install a release from GitHub and restart.
type NodeUpdate struct {
	Version string `json:"version"` // a tag such as v0.2.0; empty: the latest
	Repo    string `json:"repo"`    // owner/name on GitHub
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
	// BaseCommit is where the task's changes are measured from: the commit
	// its branch started at, or the snapshot taken before an in-place task.
	BaseCommit string `json:"base_commit,omitempty"`
}

// RunUpdate kinds.
const (
	UpdateTool    = "tool"    // a tool call started or changed
	UpdatePlan    = "plan"    // the agent's plan
	UpdateStatus  = "status"  // a short status line from sous itself
	UpdateStuck   = "stuck"   // the node saw the agent fail the same tool call again and again (Text: what)
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
	// Output is what a finished tool call returned, cut short and with
	// secrets masked. It goes to the run's log only, not the event log.
	Output string `json:"output,omitempty"`
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
	PRURL      string `json:"pr_url,omitempty"`   // deliver, git pr
	Conflict   bool   `json:"conflict,omitempty"` // deliver: the PR conflicts with its base
	// Git runs: what the operation did. Consult runs: the agent's session,
	// to load when the next question comes.
	Git       *GitResult `json:"git,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	// Outcome qualifies a work run that "succeeded": no_action when the
	// agent ended its turn without doing anything (no tool call, no change).
	Outcome string `json:"outcome,omitempty"`
	// ErrorKind classifies a failure: agent_quota, agent_rate_limit,
	// agent_not_logged_in, agent_unavailable (empty: anything else).
	ErrorKind string `json:"error_kind,omitempty"`
	// Refused lists the operations refused during the run (by the user,
	// the approver or the policy), with the reason when one was given.
	Refused []string `json:"refused,omitempty"`
}

// Run outcomes and error kinds (RunEnded).
const (
	OutcomeNoAction = "no_action"

	ErrAgentQuota       = "agent_quota"
	ErrAgentRateLimit   = "agent_rate_limit"
	ErrAgentNotLoggedIn = "agent_not_logged_in"
	ErrAgentUnavailable = "agent_unavailable"
)

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
	Category string            `json:"category,omitempty"`
	Review   string            `json:"review,omitempty"`
	Facts    *PolicyFacts      `json:"facts,omitempty"`
	Evidence *ApprovalEvidence `json:"evidence,omitempty"`
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

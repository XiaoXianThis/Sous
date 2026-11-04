package event

// Remote administration of a machine's agents (work/specs/agent-admin).
// Neither event carries secrets or file contents.
const (
	// AgentJobStarted: the user started an install, upgrade, login or
	// logout on a machine.
	AgentJobStarted Type = "agent.job.started"
	// AgentJobOutput is a piece of a job's terminal output (ephemeral).
	AgentJobOutput Type = "agent.job.output"
	AgentJobEnded  Type = "agent.job.ended"
	// NodeConfigChanged: the user changed a machine's settings: an agent's
	// default options or API key, or a config file.
	NodeConfigChanged Type = "node.config_changed"
)

type AgentJobPayload struct {
	NodeID  string `json:"node_id"`
	JobID   string `json:"job_id"`
	Kind    string `json:"kind,omitempty"` // install / upgrade / login / logout
	Agent   string `json:"agent,omitempty"`
	Command string `json:"command,omitempty"`
	// Shells: the directory and project it was opened in.
	Path      string `json:"path,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Error     string `json:"error,omitempty"`
	// agent.job.output only.
	Offset int64  `json:"offset,omitempty"`
	Data   string `json:"data,omitempty"`
}

type NodeConfigPayload struct {
	NodeID string `json:"node_id"`
	Agent  string `json:"agent"`
	// What changed: "defaults", "env:<NAME>" (set or cleared), "file:<id>".
	What string `json:"what"`
}

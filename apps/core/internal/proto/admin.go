package proto

import "encoding/json"

// Remote administration of a node's agents (work/specs/agent-admin): the
// Hub sends node.request and waits for node.response; installs and logins
// run as jobs on a pseudo-terminal whose output streams back as node.stream.
const (
	TNodeRequest  = "node.request"
	TNodeResponse = "node.response" // not acknowledged: the Hub's caller waits for it
	TNodeStream   = "node.stream"   // not acknowledged: only relayed
	TJobEnded     = "node.job_ended"
)

// CapAdmin in Hello.Caps means the node answers node.request.
const CapAdmin = "admin"

// Request ops.
const (
	OpDetect      = "agents.detect"       // -> []AgentInfo
	OpProbe       = "agents.probe"        // {agent} -> AgentInfo
	OpInstall     = "agents.install"      // {agent, upgrade, cols, rows} -> JobStarted
	OpLogin       = "agents.login"        // {agent, cols, rows} -> JobStarted
	OpLogout      = "agents.logout"       // {agent, cols, rows} -> JobStarted
	OpSetDefaults = "agents.set_defaults" // {agent, options} -> AgentInfo
	OpSetEnv      = "agents.set_env"      // {agent, name, value} -> AgentInfo
	OpJobInput    = "job.input"           // {job_id, data}
	OpJobResize   = "job.resize"          // {job_id, cols, rows}
	OpJobCancel   = "job.cancel"          // {job_id}
	OpJobTail     = "job.tail"            // {job_id} -> JobTail
	OpConfigRead  = "config.read"         // {agent, file} -> ConfigFile
	OpConfigWrite = "config.write"        // {agent, file, content, base_sha256} -> ConfigFile
)

// Error codes of node.response.
const (
	ErrCodeDisabled = "disabled" // remote administration is off in node.json
	ErrCodeConflict = "conflict" // the file changed since it was read
	ErrCodeInvalid  = "invalid"  // bad request or content
	ErrCodeNotFound = "not_found"
	ErrCodeBusy     = "busy"
	ErrCodeFailed   = "failed"
)

type NodeRequest struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

type NodeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *NodeError) Error() string { return e.Message }

type NodeResponse struct {
	ReqID  string          `json:"req_id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *NodeError      `json:"error,omitempty"`
}

// AgentArgs names the agent an op is about; the other fields depend on the op.
type AgentArgs struct {
	Agent   string            `json:"agent"`
	Upgrade bool              `json:"upgrade,omitempty"`
	Cols    int               `json:"cols,omitempty"`
	Rows    int               `json:"rows,omitempty"`
	Options map[string]string `json:"options,omitempty"` // set_defaults: config option id -> value
	Name    string            `json:"name,omitempty"`    // set_env
	Value   string            `json:"value,omitempty"`   // set_env: empty removes it
}

type JobArgs struct {
	JobID string `json:"job_id"`
	Data  string `json:"data,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

// Job kinds.
const (
	JobInstall = "install"
	JobUpgrade = "upgrade"
	JobLogin   = "login"
	JobLogout  = "logout"
)

type JobStarted struct {
	JobID   string `json:"job_id"`
	Kind    string `json:"kind"`
	Agent   string `json:"agent"`
	Command string `json:"command"` // what runs, for the user to see
}

// NodeStream is a piece of a job's terminal output. Offset counts bytes of
// output before this chunk, so a client that fetched the tail can skip what
// it already has.
type NodeStream struct {
	JobID  string `json:"job_id"`
	Offset int64  `json:"offset"`
	Data   string `json:"data"`
}

type JobTail struct {
	JobID   string `json:"job_id"`
	Kind    string `json:"kind"`
	Agent   string `json:"agent"`
	Command string `json:"command"`
	Offset  int64  `json:"offset"` // where Data starts
	Data    string `json:"data"`
	Running bool   `json:"running"`
	Exit    *int   `json:"exit_code,omitempty"`
}

type JobEnded struct {
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"`
	Agent    string `json:"agent"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

type ConfigArgs struct {
	Agent      string `json:"agent"` // "sous" for node.json
	File       string `json:"file"`
	Content    string `json:"content,omitempty"`
	BaseSHA256 string `json:"base_sha256,omitempty"`
}

type ConfigFile struct {
	Agent   string `json:"agent"`
	File    string `json:"file"`
	Path    string `json:"path"`
	Format  string `json:"format"` // json / jsonc / toml / text
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	Backup  string `json:"backup,omitempty"` // config.write: where the previous version went
}

// ConfigOption is one of an agent's session settings (ACP configOptions),
// such as its model or reasoning level.
type ConfigOption struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Category    string        `json:"category,omitempty"` // model / thought_level / model_config / mode / ...
	Current     string        `json:"current"`
	Values      []ConfigValue `json:"values"`
}

type ConfigValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
}

// AgentConfigFile is an agent config file the user may edit.
type AgentConfigFile struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Format string `json:"format"`
}

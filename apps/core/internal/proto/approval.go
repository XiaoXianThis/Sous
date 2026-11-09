package proto

type PolicyFacts struct {
	BoundedReversible bool     `json:"bounded_reversible"`
	Version           int      `json:"version"`
	PolicyVersion     string   `json:"policy_version"`
	ParseStatus       string   `json:"parse_status"`
	Role              string   `json:"role"`
	EffectiveMode     string   `json:"effective_mode"`
	CommandDigest     string   `json:"command_digest"`
	CWD               string   `json:"cwd"`
	Effects           []string `json:"effects,omitempty"`
	ReadOnlyProven    bool     `json:"read_only_proven"`
	WritesOutside     bool     `json:"writes_outside"`
	OpaqueExecution   bool     `json:"opaque_execution"`
	Hard              []string `json:"hard,omitempty"`
	HardRead          []string `json:"hard_read,omitempty"`
	Soft              []string `json:"soft,omitempty"`
	InstallSource     string   `json:"install_source"`
	EvidenceComplete  bool     `json:"evidence_complete"`
	OperationKey      string   `json:"operation_key,omitempty"`
	Reusable          bool     `json:"reusable"`
}
type ApprovalEvidence struct {
	Version  int              `json:"version"`
	Scripts  []ScriptEvidence `json:"scripts,omitempty"`
	Paths    []PathEvidence   `json:"paths,omitempty"`
	Complete bool             `json:"complete"`
	Failure  string           `json:"failure,omitempty"`
}
type ScriptEvidence struct {
	Reachability string `json:"reachability,omitempty"`
	Path         string `json:"path"`
	Language     string `json:"language"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256,omitempty"`
	ReadAt       int64  `json:"read_at"`
	Complete     bool   `json:"complete"`
	Truncated    bool   `json:"truncated"`
	Inside       bool   `json:"inside"`
	Origin       string `json:"origin"`
	Status       string `json:"status,omitempty"`
	DiffStat     string `json:"diff_stat,omitempty"`
	Content      string `json:"content,omitempty"`
	Failure      string `json:"failure,omitempty"`
}
type PathEvidence struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
}
type ApprovalReadArgs struct {
	RunID      string `json:"run_id"`
	ApprovalID string `json:"approval_id"`
	Path       string `json:"path"`
	Offset     int    `json:"offset"`
	MaxBytes   int    `json:"max_bytes"`
}
type ApprovalReadResult struct {
	Content  string `json:"content"`
	Size     int    `json:"size"`
	SHA256   string `json:"sha256"`
	Complete bool   `json:"complete"`
}

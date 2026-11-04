package proto

// Milestone M5 (work/specs/m5-workbench): workspaces and the repositories
// found in them, machine roles, personas' session options, project setup
// runs (clone, create) and operations runs on servers.

const (
	// TNodeRepos reports the git repositories in the node's workspaces,
	// all of them at once. Reliable: the Hub stores it.
	TNodeRepos = "node.repos"
)

// Machine roles.
const (
	RoleDev    = "dev"    // a developer's computer: code and general tasks
	RoleServer = "server" // a server: operations tasks such as deploying
)

// Run kinds beyond code and general.
const (
	KindOps = "ops" // in place, in a project's directory on a server
)

// Purposes of setup runs: the node itself runs fixed commands, with no agent.
const (
	PurposeClone  = "clone"  // git clone a project into the first workspace
	PurposeCreate = "create" // create a new project (wok init) in the first workspace
)

// Repo is one git repository found in a workspace.
type Repo struct {
	Path     string `json:"path"`
	Remote   string `json:"remote,omitempty"` // origin, else the first remote
	Branch   string `json:"branch,omitempty"`
	Dirty    bool   `json:"dirty,omitempty"`
	Wokspace bool   `json:"wokspace,omitempty"` // AGENTS.md plus apps/ build/ docs/ work/
}

type NodeRepos struct {
	Workspaces []string `json:"workspaces"`
	Repos      []Repo   `json:"repos"`
}

// Setup says what a clone or create run does.
type Setup struct {
	// Name is the directory under the first workspace (kebab-case).
	Name string `json:"name"`
	// Remote is the repository to clone (clone).
	Remote string `json:"remote,omitempty"`
	// Layout is "wokspace" to start from `wok init`, or empty for `git init`
	// (create).
	Layout string `json:"layout,omitempty"`
	// Owner, when set, creates a GitHub repository owner/name and pushes to
	// it (create). Private makes it private.
	Owner   string `json:"owner,omitempty"`
	Private bool   `json:"private,omitempty"`
}

// Remote administration ops added in M5.
const (
	OpSetWorkspaces = "node.set_workspaces" // {workspaces} -> NodeRepos
	OpSetRole       = "node.set_role"       // {role}
	OpPRState       = "gh.pr_state"         // {url} -> PRState
	OpScan          = "repos.scan"          // -> NodeRepos
)

type WorkspacesArgs struct {
	Workspaces []string `json:"workspaces"`
}

type RoleArgs struct {
	Role string `json:"role"`
}

type PRArgs struct {
	URL string `json:"url"`
}

type PRState struct {
	State    string `json:"state"` // OPEN / CLOSED / MERGED
	MergedAt string `json:"merged_at,omitempty"`
}

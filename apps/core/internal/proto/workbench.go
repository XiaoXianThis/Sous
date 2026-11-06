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
	KindOps = "ops" // in place, in a project's directory (kept for old runs)
	// KindInPlace works in a project's directory itself; the node takes a
	// snapshot first so the changes can be shown and undone (decision 0022).
	KindInPlace = "in_place"
)

// Purposes of setup runs: the node itself runs fixed commands, with no agent.
const (
	PurposeClone  = "clone"  // git clone a project into the first workspace
	PurposeCreate = "create" // create a new project (wok init) in the first workspace
)

// Repo is one git repository found in a workspace.
type Repo struct {
	Path     string    `json:"path"`
	Remote   string    `json:"remote,omitempty"` // origin, else the first remote
	Branch   string    `json:"branch,omitempty"`
	Dirty    bool      `json:"dirty,omitempty"`
	Wokspace bool      `json:"wokspace,omitempty"` // AGENTS.md plus apps/ build/ docs/ work/
	Git      *GitState `json:"git,omitempty"`
	// Heads are the remote-tracking branches of origin and their commits
	// ("main": "<sha>"), as of the last fetch or push; for deploy branches.
	Heads map[string]string `json:"heads,omitempty"`
}

// GitState is where a checkout stands against its upstream
// (work/specs/multi-machine-sync, R2). Read without network access; Behind
// is as of the last fetch (FetchedAt).
type GitState struct {
	Head      string `json:"head,omitempty"`
	Upstream  string `json:"upstream,omitempty"` // origin/main; empty when the branch tracks nothing
	Ahead     int    `json:"ahead"`              // commits not pushed
	Behind    int    `json:"behind"`             // commits on the upstream not here
	Changes   int    `json:"changes"`            // tracked files with uncommitted changes
	Untracked int    `json:"untracked"`
	FetchedAt int64  `json:"fetched_at,omitempty"` // unix ms of the last fetch
}

// Repository operations from the page (node.request).
const (
	OpRepoSync = "repo.sync" // {root} -> RepoResult
	OpRepoWIP  = "repo.wip"  // {root} -> RepoResult
)

type RepoArgs struct {
	Root string `json:"root"`
}

// RepoResult is what repo.sync or repo.wip did.
type RepoResult struct {
	// Outcome: up_to_date / fast_forwarded / dirty / diverged / no_upstream
	// (sync); pushed / nothing (wip).
	Outcome string    `json:"outcome"`
	Message string    `json:"message"`          // for the user
	Branch  string    `json:"branch,omitempty"` // wip: the branch pushed
	Commit  string    `json:"commit,omitempty"`
	Git     *GitState `json:"git,omitempty"` // the checkout afterwards
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
	// Decision 0022.
	OpGitStatus   = "git.status"   // {root} -> GitStatus
	OpProjectGrep = "project.grep" // {root, rel, pattern} -> GrepResult
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

// Git operations of a git run (decision 0022). Every parameter is explicit:
// the node never picks a branch or a remote by itself.
const (
	GitCommit  = "commit"  // commit what is left in the task's worktree (Squash: one commit since BaseCommit)
	GitMerge   = "merge"   // rebase the task branch onto Into, then fast-forward Into
	GitPush    = "push"    // push Branch to Remote (no force)
	GitPR      = "pr"      // push the task branch and open a pull request against Base
	GitUndo    = "undo"    // undo the last operation sous did on Branch that is not pushed
	GitDiscard = "discard" // delete the task's worktree and branch
	GitRestore = "restore" // put an in-place task's directory back to its snapshot
)

// GitOp is what a git run does. Repository is RunStart.RepoPath; a task's
// worktree and branch come from RunStart.TaskID and Branch.
type GitOp struct {
	Op         string `json:"op"`
	Into       string `json:"into,omitempty"`        // merge: the branch to fast-forward
	Base       string `json:"base,omitempty"`        // pr: the pull request's base branch
	Branch     string `json:"branch,omitempty"`      // push, undo: the branch
	Remote     string `json:"remote,omitempty"`      // push: the remote; empty only when it is unambiguous
	Squash     bool   `json:"squash,omitempty"`      // commit, merge: fold the task's commits into one
	Message    string `json:"message,omitempty"`     // commit, merge (squash): the commit message
	Title      string `json:"title,omitempty"`       // pr
	Body       string `json:"body,omitempty"`        // pr
	BaseCommit string `json:"base_commit,omitempty"` // where the task started (squash, restore)
	InPlace    bool   `json:"in_place,omitempty"`    // the task worked in the project directory
}

// GitResult is what a git run did.
type GitResult struct {
	Op        string   `json:"op"`
	Branch    string   `json:"branch,omitempty"` // the branch that moved
	Before    string   `json:"before,omitempty"`
	After     string   `json:"after,omitempty"`
	Commits   int      `json:"commits,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"` // merge: files in conflict; nothing was changed
	PRURL     string   `json:"pr_url,omitempty"`
	Remote    string   `json:"remote,omitempty"`
	// TaskBranch is the task's branch as it was when a merge deleted it, so
	// an undo can bring it back.
	TaskBranch string `json:"task_branch,omitempty"`
	Message    string `json:"message"` // one sentence for the user
}

// GitStatus is where a checkout stands right now, for the supervisor to
// judge which branch is meant (decision 0022). Read without network access.
type GitStatus struct {
	Root          string      `json:"root"`
	Branch        string      `json:"branch,omitempty"` // empty when detached
	Detached      bool        `json:"detached,omitempty"`
	Head          string      `json:"head,omitempty"`
	Upstream      string      `json:"upstream,omitempty"`
	Ahead         int         `json:"ahead"`
	Behind        int         `json:"behind"`
	Changes       int         `json:"changes"`
	Untracked     int         `json:"untracked"`
	RemoteDefault string      `json:"remote_default,omitempty"` // for reference only, never a default
	Remotes       []string    `json:"remotes,omitempty"`
	Branches      []GitBranch `json:"branches,omitempty"` // recently active local branches
	FetchedAt     int64       `json:"fetched_at,omitempty"`
}

type GitBranch struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	When     int64  `json:"when"` // unix seconds of the last commit
	Subject  string `json:"subject,omitempty"`
	// Worktree is where the branch is checked out, if anywhere.
	Worktree string `json:"worktree,omitempty"`
}

// GrepArgs searches a project directory: Pattern is a Go regular
// expression, Rel narrows the search to a subdirectory.
type GrepArgs struct {
	Root    string `json:"root"`
	Rel     string `json:"rel,omitempty"`
	Pattern string `json:"pattern"`
}

type GrepMatch struct {
	Rel  string `json:"rel"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type GrepResult struct {
	Matches   []GrepMatch `json:"matches"`
	Truncated bool        `json:"truncated,omitempty"`
	Files     int         `json:"files"` // files searched
}

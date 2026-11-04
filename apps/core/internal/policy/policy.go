// Package policy classifies what a coding agent asks to do and decides, by
// permission mode, whether the node allows it, asks the user, asks the AI
// approver, or refuses it (design §8, docs/decisions/0005).
//
// This is a safety net, not isolation (docs/decisions/0010): it sees only
// the actions an agent asks permission for, and the floor rules match
// command text, which a rephrased command can slip past.
package policy

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Category is a class of action.
type Category string

const (
	Read            Category = "read"
	EditWorktree    Category = "edit-worktree"
	ExecSafe        Category = "exec-safe"
	ExecOther       Category = "exec-other"
	Install         Category = "install"
	OutsideWorktree Category = "outside-worktree"
	Delete          Category = "delete"
	GitRemote       Category = "git-remote"
	Service         Category = "service" // start, stop or restart a service (M5)
	Irreversible    Category = "irreversible"
	Floor           Category = "floor"
)

// rank orders categories by risk, so a compound command takes its worst part.
var rank = map[Category]int{
	Read: 0, EditWorktree: 1, ExecSafe: 2, ExecOther: 3, Install: 4, OutsideWorktree: 5,
	Delete: 6, GitRemote: 7, Service: 8, Irreversible: 9, Floor: 10,
}

// Action is what the node does with a request.
type Action string

const (
	Allow   Action = "allow"
	AskUser Action = "user"
	AskAI   Action = "ai"
	Deny    Action = "deny"
)

// Permission modes, as in proto.
const (
	ModeDefault = "default"
	ModeAuto    = "auto"
	ModeOpen    = "open"
)

// Decide looks up the table in design §8.
func Decide(mode string, c Category) Action {
	switch c {
	case Floor:
		return Deny
	case Read, EditWorktree, ExecSafe:
		return Allow
	}
	switch mode {
	case ModeOpen:
		return Allow
	case ModeAuto:
		if c == Irreversible {
			return AskUser
		}
		return AskAI
	}
	return AskUser
}

// Machine roles, as in proto.
const (
	RoleDev    = "dev"
	RoleServer = "server"
)

// EffectiveMode is the mode a run really gets on a machine: servers never
// run in the open mode (docs/decisions/0014).
func EffectiveMode(role, mode string) string {
	if role == RoleServer && mode == ModeOpen {
		return ModeDefault
	}
	return mode
}

// DecideFor is Decide for a machine's role. On a server, edits, deletions,
// service changes and irreversible actions always go to the user, never to
// the AI approver, and the open mode is not available. Only node.json can
// change a machine's role back to dev.
func DecideFor(role, mode string, c Category) Action {
	mode = EffectiveMode(role, mode)
	if role == RoleServer {
		switch c {
		case Floor:
			return Deny
		case EditWorktree, Delete, Service, Irreversible:
			return AskUser
		}
	}
	return Decide(mode, c)
}

// DefaultFloor is the floor rules a node starts with: actions refused in
// every mode. Patterns are Go regular expressions over the command text.
var DefaultFloor = []string{
	`\brm\s+(-[a-zA-Z]*\s+)*(/|~|\$HOME|/\*|~/\*)(\s|$)`,    // delete the root or home directory
	`\brm\s+-[a-zA-Z]*[rR][a-zA-Z]*\s+.*--no-preserve-root`, // rm -rf --no-preserve-root
	`\bmkfs(\.\w+)?\b`,                                          // format a file system
	`\bdd\b.*\bof=/dev/(disk|sd|nvme|hd|rdisk)`,                 // overwrite a disk
	`\bdiskutil\s+(erase|zero|secureErase|partitionDisk)`,       // erase a disk on macOS
	`(?i)\bformat(\.com)?\s+[a-z]:`,                             // format a Windows drive
	`(?i)Remove-Item\s+.*-Recurse.*\s([a-z]:\\|~|\$HOME)(\s|$)`, // PowerShell delete of a drive or home
	`:\(\)\s*\{\s*:\|:&\s*\};:`,                                 // fork bomb
	`\b(shutdown|reboot|halt|poweroff)\b`,                       // stop the machine
	`\bchmod\s+(-R\s+)?[0-7]*777\s+/(\s|$)`,                     // open up the whole disk
	`>\s*/dev/(disk|sd|nvme)`,                                   // write to a raw disk
	`\bcurl\b[^|]*\|\s*(sudo\s+)?(ba|z)?sh\b`,                   // pipe a download into a shell
	`\bwget\b[^|]*\|\s*(sudo\s+)?(ba|z)?sh\b`,                   //
}

// Policy classifies requests for one run.
type Policy struct {
	floor []*regexp.Regexp
}

// Config is the policy section of a node's config.
type Config struct {
	// FloorDisabled turns the floor rules off (requirements R5.2).
	FloorDisabled bool `json:"floor_disabled,omitempty"`
	// Floor replaces the default floor rules when set.
	Floor []string `json:"floor,omitempty"`
	// ExtraFloor adds rules to the defaults.
	ExtraFloor []string `json:"extra_floor,omitempty"`
}

// New compiles a policy. Invalid patterns are returned as errors.
func New(c Config) (*Policy, error) {
	p := &Policy{}
	if c.FloorDisabled {
		return p, nil
	}
	rules := DefaultFloor
	if len(c.Floor) > 0 {
		rules = c.Floor
	}
	for _, r := range append(append([]string{}, rules...), c.ExtraFloor...) {
		re, err := regexp.Compile(r)
		if err != nil {
			return nil, err
		}
		p.floor = append(p.floor, re)
	}
	return p, nil
}

// Request is an agent's permission request.
type Request struct {
	ToolKind string // ACP tool kind: read, edit, delete, move, search, execute, think, fetch, other
	Title    string
	RawInput json.RawMessage
	Paths    []string // locations the tool call names
	Workdir  string
}

// Classify returns the category of a request and a short reason.
func (p *Policy) Classify(r Request) (Category, string) {
	in := parseInput(r.RawInput)
	paths := append(append([]string{}, r.Paths...), in.paths...)
	cmd := in.command
	if cmd == "" && r.ToolKind == "execute" {
		cmd = r.Title // some agents only put the command in the title
	}
	if cmd != "" {
		if rule := p.floorHit(cmd); rule != "" {
			return Floor, "命中底线规则：" + rule
		}
	}

	switch r.ToolKind {
	case "read", "search", "think", "fetch":
		if out := outside(paths, r.Workdir); out != "" {
			return OutsideWorktree, "读取工作目录之外的 " + out
		}
		return Read, ""
	case "edit", "move":
		if out := outside(paths, r.Workdir); out != "" {
			return OutsideWorktree, "修改工作目录之外的 " + out
		}
		return EditWorktree, ""
	case "delete":
		if out := outside(paths, r.Workdir); out != "" {
			return OutsideWorktree, "删除工作目录之外的 " + out
		}
		return Delete, ""
	case "execute":
		if cmd == "" {
			return ExecOther, "无法识别的命令"
		}
		return classifyCommand(cmd, r.Workdir)
	}
	if cmd != "" {
		return classifyCommand(cmd, r.Workdir)
	}
	return ExecOther, "未知类型的操作"
}

func (p *Policy) floorHit(cmd string) string {
	for _, re := range p.floor {
		if re.MatchString(cmd) {
			return re.String()
		}
	}
	return ""
}

type input struct {
	command string
	paths   []string
}

// parseInput pulls a command line and file paths out of a tool's raw input,
// whose shape differs per agent.
func parseInput(raw json.RawMessage) input {
	var in input
	if len(raw) == 0 {
		return in
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return in
	}
	for _, k := range []string{"command", "cmd", "commandLine", "script"} {
		switch v := m[k].(type) {
		case string:
			in.command = v
		case []any:
			var parts []string
			for _, x := range v {
				if s, ok := x.(string); ok {
					parts = append(parts, s)
				}
			}
			// ["bash", "-lc", "go test ./..."] runs its last part.
			if len(parts) >= 3 && (parts[1] == "-c" || parts[1] == "-lc") {
				in.command = parts[len(parts)-1]
			} else {
				in.command = strings.Join(parts, " ")
			}
		}
		if in.command != "" {
			break
		}
	}
	for _, k := range []string{"path", "file_path", "filePath", "abs_path", "notebook_path", "target", "destination", "source", "cwd", "workdir"} {
		if s, ok := m[k].(string); ok && s != "" {
			in.paths = append(in.paths, s)
		}
	}
	if ps, ok := m["paths"].([]any); ok {
		for _, x := range ps {
			if s, ok := x.(string); ok {
				in.paths = append(in.paths, s)
			}
		}
	}
	return in
}

// outside returns the first path that is not inside workdir, or "".
func outside(paths []string, workdir string) string {
	if workdir == "" {
		return ""
	}
	root := filepath.Clean(workdir)
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs := p
		if !filepath.IsAbs(abs) {
			if strings.HasPrefix(p, "~") {
				return p
			}
			abs = filepath.Join(root, p)
		}
		rel, err := filepath.Rel(root, filepath.Clean(abs))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return p
		}
	}
	return ""
}

var splitter = regexp.MustCompile(`&&|\|\||;|\||\n`)

// classifyCommand classifies a shell command line by its worst part.
func classifyCommand(cmd, workdir string) (Category, string) {
	worst, why := Read, ""
	for _, part := range splitter.Split(cmd, -1) {
		c, w := classifyOne(strings.TrimSpace(part), workdir)
		if rank[c] > rank[worst] {
			worst, why = c, w
		}
	}
	if why == "" && worst != Read && worst != ExecSafe {
		why = cmd
	}
	return worst, why
}

func classifyOne(cmd, workdir string) (Category, string) {
	f := strings.Fields(cmd)
	// Skip environment assignments like FOO=1 go test.
	for len(f) > 0 && strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "-") {
		f = f[1:]
	}
	if len(f) == 0 {
		return Read, ""
	}
	name := filepath.Base(f[0])
	args := f[1:]
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	has := func(flags ...string) bool {
		for _, a := range args {
			for _, fl := range flags {
				if a == fl || strings.HasPrefix(a, fl+"=") {
					return true
				}
			}
		}
		return false
	}

	// Commands that reach outside the working directory.
	if name == "cd" {
		if out := outside(args, workdir); out != "" {
			return OutsideWorktree, "进入工作目录之外的 " + out
		}
		return Read, ""
	}
	for _, a := range args {
		if filepath.IsAbs(a) || strings.HasPrefix(a, "~/") {
			if out := outside([]string{a}, workdir); out != "" && !readOnly[name] && !isSystemPath(a) {
				return OutsideWorktree, cmd
			}
		}
	}

	switch name {
	case "sudo", "su", "doas", "runas":
		return Irreversible, cmd
	case "git":
		switch sub {
		case "push":
			if has("--force", "-f", "--delete", "-d", "--mirror") || pushesDeletion(args) {
				return Irreversible, cmd
			}
			return GitRemote, cmd
		case "status", "diff", "log", "show", "branch", "rev-parse", "ls-files", "blame", "remote", "describe", "grep", "config":
			if sub == "branch" && has("-D", "-d", "--delete") {
				return Irreversible, cmd
			}
			return Read, ""
		case "add", "commit", "checkout", "switch", "restore", "stash", "merge", "rebase", "cherry-pick", "mv", "fetch", "pull", "tag":
			if sub == "tag" && has("-d", "--delete") {
				return Irreversible, cmd
			}
			return EditWorktree, ""
		case "rm", "clean":
			return Delete, cmd
		case "reset":
			if has("--hard") {
				return Delete, cmd
			}
			return EditWorktree, ""
		}
		return ExecOther, cmd
	case "gh":
		if sub == "pr" || sub == "release" || sub == "repo" || sub == "api" {
			if len(args) > 1 && (args[1] == "view" || args[1] == "list" || args[1] == "status" || args[1] == "diff" || args[1] == "checks") {
				return Read, ""
			}
			if sub == "release" || sub == "repo" && len(args) > 1 && args[1] == "delete" {
				return Irreversible, cmd
			}
			return GitRemote, cmd
		}
		return ExecOther, cmd
	case "rm", "rmdir", "unlink", "del", "rd", "Remove-Item", "shred":
		return Delete, cmd
	case "npm", "pnpm", "yarn", "bun":
		switch sub {
		case "publish", "unpublish", "deprecate":
			return Irreversible, cmd
		case "install", "i", "add", "ci", "update", "upgrade", "remove", "uninstall":
			if sub == "ci" || (sub == "install" || sub == "i") && len(args) == 1 {
				return ExecSafe, "" // install what the lock file already lists
			}
			return Install, cmd
		case "test", "t", "run", "exec", "x":
			if sub == "run" && len(args) > 1 && !safeScript[args[1]] {
				return ExecOther, cmd
			}
			return ExecSafe, ""
		}
		return ExecOther, cmd
	case "pip", "pip3", "uv", "poetry", "brew", "apt", "apt-get", "yum", "dnf", "pacman", "choco", "winget", "scoop", "gem", "conda":
		if sub == "list" || sub == "show" || sub == "freeze" || sub == "info" || sub == "search" {
			return Read, ""
		}
		if name == "uv" && sub == "run" || name == "poetry" && sub == "run" {
			return ExecSafe, ""
		}
		return Install, cmd
	case "go":
		switch sub {
		case "test", "build", "vet", "fmt", "run", "generate", "list", "env", "version", "doc", "mod":
			if sub == "mod" && len(args) > 1 && args[1] != "tidy" && args[1] != "download" && args[1] != "verify" {
				return ExecOther, cmd
			}
			return ExecSafe, ""
		case "get", "install":
			return Install, cmd
		}
		return ExecOther, cmd
	case "cargo":
		switch sub {
		case "test", "build", "check", "clippy", "fmt", "run", "doc", "bench", "tree":
			return ExecSafe, ""
		case "add", "install", "update", "remove":
			return Install, cmd
		case "publish", "yank":
			return Irreversible, cmd
		}
		return ExecOther, cmd
	case "systemctl", "service", "launchctl", "supervisorctl", "pm2", "rc-service", "sv":
		if name == "systemctl" && (sub == "status" || sub == "is-active" || sub == "list-units" || sub == "show" || sub == "cat") ||
			name == "pm2" && (sub == "list" || sub == "ls" || sub == "status" || sub == "logs") ||
			name == "launchctl" && (sub == "list" || sub == "print") {
			return Read, ""
		}
		return Service, cmd
	case "docker", "podman":
		if sub == "compose" && len(args) > 1 {
			sub = args[1]
		} else if name == "docker" && sub == "compose" {
			return ExecOther, cmd
		}
		switch sub {
		case "up", "down", "restart", "stop", "start", "kill", "rm", "run":
			return Service, cmd
		case "ps", "logs", "images", "inspect", "version", "info", "config":
			return Read, ""
		}
		return ExecOther, cmd
	case "docker-compose":
		switch sub {
		case "up", "down", "restart", "stop", "start", "kill", "rm", "run":
			return Service, cmd
		}
		return ExecOther, cmd
	case "kubectl", "terraform", "helm":
		return ExecOther, cmd
	}
	if readOnly[name] {
		return Read, ""
	}
	if safeTools[name] {
		return ExecSafe, ""
	}
	return ExecOther, cmd
}

func pushesDeletion(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, ":") || strings.HasPrefix(a, "+") {
			return true
		}
	}
	return false
}

func isSystemPath(p string) bool {
	for _, prefix := range []string{"/usr/", "/bin/", "/opt/homebrew/", "/dev/null", "/tmp/", "/var/folders/", "/private/tmp/", "/etc/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// readOnly commands only look at things.
var readOnly = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "rg": true, "find": true, "fd": true, "pwd": true,
	"echo": true, "printf": true, "wc": true, "which": true, "whereis": true, "type": true, "file": true, "stat": true,
	"du": true, "df": true, "tree": true, "less": true, "more": true, "diff": true, "cmp": true, "sort": true, "uniq": true,
	"cut": true, "awk": true, "jq": true, "date": true, "whoami": true, "uname": true, "env": true, "printenv": true,
	"ps": true, "top": true, "true": true, "false": true, "test": true, "[": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "sha256sum": true, "shasum": true, "md5": true, "md5sum": true, "Get-ChildItem": true,
	"Get-Content": true, "dir": true, "sed": true,
}

// safeTools build, test and check code inside the worktree.
var safeTools = map[string]bool{
	"make": true, "pytest": true, "tsc": true, "vitest": true, "jest": true,
	"eslint": true, "prettier": true, "gofmt": true, "golangci-lint": true, "ruff": true, "mypy": true, "black": true,
	"mvn": true, "gradle": true, "./gradlew": true, "dotnet": true, "swift": true, "xcodebuild": true, "deno": true,
	"just": true, "wok": true, "mkdir": true, "touch": true, "cp": true, "mv": true, "tee": true,
	"svelte-check": true, "vite": true, "rustc": true, "gcc": true, "clang": true, "cmake": true, "ninja": true,
}

// safeScript are npm scripts that only build, test or check.
var safeScript = map[string]bool{
	"test": true, "build": true, "lint": true, "check": true, "format": true, "fmt": true, "typecheck": true, "dev": true,
	"test:unit": true, "test:e2e": true, "preview": true,
}

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
	// Decision 0022: the node sets these for consult and review runs. They
	// never ask anyone: what they do not allow is refused, and the agent is
	// told why.
	ModeReadOnly = "readonly" // reading only
	ModeVerify   = "verify"   // reading plus safe commands such as tests
)

// Restricted reports whether a mode is one of the refuse-everything-else
// modes that the Hub cannot loosen.
func Restricted(mode string) bool { return mode == ModeReadOnly || mode == ModeVerify }

// Decide looks up the table in design §8.
func Decide(mode string, c Category) Action {
	switch mode {
	case ModeReadOnly:
		if c == Read {
			return Allow
		}
		return Deny
	case ModeVerify:
		if c == Read || c == ExecSafe {
			return Allow
		}
		return Deny
	}
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
	if Restricted(mode) {
		return Decide(mode, c)
	}
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
	floor       []*regexp.Regexp
	extraHard   []*regexp.Regexp
	noRoutine   bool
	legacyShell bool
	noHardRead  bool
}

// Config is the policy section of a node's config.
type Config struct {
	LegacyShell      bool `json:"legacy_shell,omitempty"`
	HardReadDisabled bool `json:"hard_read_disabled,omitempty"`
	// FloorDisabled turns the floor rules off (requirements R5.2).
	FloorDisabled bool `json:"floor_disabled,omitempty"`
	// Floor replaces the default floor rules when set.
	Floor []string `json:"floor,omitempty"`
	// ExtraFloor adds rules to the defaults.
	ExtraFloor []string `json:"extra_floor,omitempty"`
	// ExtraHard adds high-risk signals: requests matching one always go to
	// the user, never to the approver agent (docs/decisions/0019).
	ExtraHard []string `json:"extra_hard,omitempty"`
	// RoutineDisabled sends routine actions (pushing a task branch, deleting
	// build output) to the approver agent in the auto mode too.
	RoutineDisabled bool `json:"routine_disabled,omitempty"`
}

// New compiles a policy. Invalid patterns are returned as errors.
func New(c Config) (*Policy, error) {
	p := &Policy{noRoutine: c.RoutineDisabled, legacyShell: c.LegacyShell, noHardRead: c.HardReadDisabled}
	for _, r := range c.ExtraHard {
		re, err := regexp.Compile(r)
		if err != nil {
			return nil, err
		}
		p.extraHard = append(p.extraHard, re)
	}
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
	UnsetEnv []string // node-derived wrapper facts for a script; never from rawInput
	ToolKind string   // ACP tool kind: read, edit, delete, move, search, execute, think, fetch, other
	Title    string
	RawInput json.RawMessage
	Paths    []string // locations the tool call names
	Workdir  string
	CWD      string // execution cwd, distinct from the trust root
	Shell    string // empty uses the platform shell; unsupported syntax falls back
}

// Classify returns the category of a request and a short reason.
func (p *Policy) Classify(r Request) (Category, string) {
	r = ResolveRequest(r)
	in := parseInput(r.RawInput)
	paths := append(append([]string{}, r.Paths...), in.paths...)
	cmd := in.command
	if cmd == "" && r.ToolKind == "execute" {
		cmd = r.Title // some agents only put the command in the title
	}
	if cmd != "" {
		if rule := p.floorHit(cmd + "\n" + in.floorText); rule != "" {
			return Floor, "命中底线规则：" + rule
		}
	}

	if len(r.UnsetEnv) > 0 {
		cmd = ReachableShell(cmd, r.UnsetEnv)
	}
	if cmd != "" {
		var c Category
		var why string
		if p.legacyShell {
			c, why = classifyCommandLegacy(cmd, r.Workdir)
		} else {
			c, why = classifyRequestCommand(cmd, r)
		}
		if outside(paths, r.Workdir) != "" && rank[c] < rank[OutsideWorktree] {
			c = OutsideWorktree
			why = "工具声明了工作目录之外的路径"
		}
		if (r.ToolKind == "edit" || r.ToolKind == "move") && rank[c] < rank[EditWorktree] {
			c = EditWorktree
		}
		if r.ToolKind == "delete" && rank[c] < rank[Delete] {
			c = Delete
		}
		return c, why
	}

	switch r.ToolKind {
	case "read", "search", "think", "fetch":
		if out := outside(paths, r.Workdir); out != "" {
			return OutsideWorktree, "读取工作目录之外的 " + out
		}
		if cmd != "" {
			// A "read" that carries a command line is judged by the command:
			// the kind is the agent's word, the command is what runs (review
			// M2; the read-only modes rely on this).
			if p.legacyShell {
				return classifyCommandLegacy(cmd, r.Workdir)
			}
			return classifyRequestCommand(cmd, r)
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
		if p.legacyShell {
			return classifyCommandLegacy(cmd, r.Workdir)
		}
		return classifyRequestCommand(cmd, r)
	}
	if cmd != "" {
		if p.legacyShell {
			return classifyCommandLegacy(cmd, r.Workdir)
		}
		return classifyRequestCommand(cmd, r)
	}
	return ExecOther, "未知类型的操作"
}

// CommandOf is the command line a request runs, as Classify reads it ("" for
// a request that is not a command).
func CommandOf(r Request) string {
	cmd := parseInput(r.RawInput).command
	if cmd == "" && r.ToolKind == "execute" {
		cmd = r.Title
	}
	return cmd
}

func RequestPaths(r Request) []string {
	return append(append([]string{}, r.Paths...), parseInput(r.RawInput).paths...)
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
	floorText  string
	command    string
	paths      []string
	cwd, shell string
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
			in.floorText = strings.Join(parts, " ")
			for i, part := range parts {
				parts[i] = "'" + strings.ReplaceAll(part, "'", "'\"'\"'") + "'"
			}
			in.command = strings.Join(parts, " ")
		}
		if in.command != "" {
			break
		}
	}
	for _, k := range []string{"cwd", "workdir"} {
		if v, ok := m[k].(string); ok && v != "" {
			in.cwd = v
			break
		}
	}
	if v, ok := m["shell"].(string); ok {
		in.shell = v
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
func classifyCommandLegacy(cmd, workdir string) (Category, string) {
	return classifyLegacy(cmd, workdir, 0)
}
func classifyLegacy(cmd, workdir string, depth int) (Category, string) {
	if depth >= 16 || len(cmd) > 256<<10 {
		return ExecOther, "旧解析器预算耗尽"
	}
	worst, why := Read, ""
	// Commands inside $(...), `...` and <(...) run too: classify them as
	// commands of their own.
	cmd, subs, open := substitutions(cmd)
	if open {
		worst, why = ExecOther, "无法解析的命令替换"
	}
	for _, sub := range subs {
		if c, w := classifyLegacy(sub, workdir, depth+1); rank[c] > rank[worst] {
			worst, why = c, w
		}
	}
	for _, part := range splitter.Split(cmd, -1) {
		c, w := classifyOneDepth(strings.TrimSpace(part), workdir, depth+1)
		if rank[c] > rank[worst] {
			worst, why = c, w
		}
	}
	if why == "" && worst != Read && worst != ExecSafe {
		why = cmd
	}
	return worst, why
}

// classifyOne classifies one simple command, then raises the result when
// it redirects output into a file.
func classifyOne(cmd, workdir string) (Category, string) {
	return classifyOneDepth(cmd, workdir, 0)
}
func classifyOneDepth(cmd, workdir string, depth int) (Category, string) {
	if depth >= 16 {
		return ExecOther, "旧解析器递归预算耗尽"
	}
	c, w := classifyBaseDepth(cmd, workdir, depth)
	if rc, rw := redirectCategory(cmd, workdir); rank[rc] > rank[c] {
		return rc, rw
	}
	return c, w
}

var shellC = regexp.MustCompile(`(?s)^(?:\S*/)?(?:ba|z|da|k)?sh\s+(?:-[a-z]+\s+)*-[a-z]*c\s+(?:'(.*)'|"(.*)")\s*$`)

// wrappers run another command given as their arguments, with these
// options taking a value.
var wrappers = map[string]string{
	"env": "-uCS", "nohup": "", "time": "", "command": "", "exec": "", "nice": "-n", "stdbuf": "-ioe",
	"timeout": "-ks", "xargs": "-nIPLdEsa", "ionice": "-cn", "setsid": "", "caffeinate": "",
}

// unwrap returns the command a wrapper like env, timeout or xargs runs.
func unwrap(f []string) []string {
	valued, ok := wrappers[filepath.Base(f[0])]
	if !ok {
		return nil
	}
	name := filepath.Base(f[0])
	rest := f[1:]
	for len(rest) > 0 {
		a := rest[0]
		switch {
		case strings.HasPrefix(a, "-") && len(a) >= 2:
			rest = rest[1:]
			if len(a) == 2 && strings.Contains(valued, a[1:]) && len(rest) > 0 {
				rest = rest[1:] // the option's value
			}
		case name == "env" && strings.Contains(a, "="):
			rest = rest[1:]
		case name == "timeout":
			name = "" // the duration, once
			rest = rest[1:]
		default:
			return rest
		}
	}
	return rest
}

// redirectCategory looks at > and >> in a simple command: writing into a
// file is at least an edit, and outside the working directory is not one.
func redirectCategory(cmd, workdir string) (Category, string) {
	f := strings.Fields(cmd)
	worst, why := Read, ""
	for i, tok := range f {
		m := redirect.FindStringSubmatch(tok)
		if m == nil {
			continue
		}
		target := m[3]
		if target == "" && i+1 < len(f) {
			target = f[i+1]
		}
		target = strings.Trim(target, `"'`)
		if target == "" || strings.HasPrefix(target, "&") || target == "/dev/null" || target == "/dev/stdout" || target == "/dev/stderr" {
			continue
		}
		if out := outside([]string{target}, workdir); out != "" {
			return OutsideWorktree, "把输出写到工作目录之外的 " + out
		}
		worst, why = EditWorktree, ""
	}
	return worst, why
}

var redirect = regexp.MustCompile(`^(\d*|&)(>>?|>\|)(.*)$`)

func classifyBase(cmd, workdir string) (Category, string) {
	return classifyBaseDepth(cmd, workdir, 0)
}
func classifyBaseDepth(cmd, workdir string, depth int) (Category, string) {
	if m := shellC.FindStringSubmatch(strings.TrimSpace(cmd)); m != nil {
		return classifyLegacy(m[1]+m[2], workdir, depth+1)
	}
	f := strings.Fields(cmd)
	// Skip environment assignments like FOO=1 go test.
	for len(f) > 0 && strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "-") {
		f = f[1:]
	}
	if len(f) == 0 {
		return Read, ""
	}
	if isWrapper(f[0]) {
		if filepath.Base(f[0]) == "command" && len(f) > 1 && (f[1] == "-v" || f[1] == "-V") {
			return Read, ""
		}
		inner := unwrap(f)
		if len(inner) == 0 {
			return Read, "" // env alone prints the environment
		}
		return classifyOneDepth(strings.Join(inner, " "), workdir, depth+1)
	}
	return classifyArgvDepth(f, cmd, workdir, depth)
}

// classifyArgv consumes decoded argv; it must never split quoted arguments again.
func classifyArgv(f []string, cmd, workdir string) (Category, string) {
	return classifyArgvDepth(f, cmd, workdir, 0)
}
func classifyArgvDepth(f []string, cmd, workdir string, depth int) (Category, string) {
	if len(f) == 0 {
		return Read, ""
	}
	name := filepath.Base(f[0])
	if name == "git" && len(f) > 1 && strings.HasPrefix(f[1], "-") {
		i := 1
		var roots []string
		for i < len(f) && strings.HasPrefix(f[i], "-") {
			v := f[i]
			switch {
			case v == "--no-pager" || v == "--paginate":
				i++
			case v == "-C" || v == "-c" || v == "--git-dir" || v == "--work-tree":
				if i+1 >= len(f) {
					return ExecOther, cmd
				}
				if v != "-c" {
					roots = append(roots, f[i+1])
				}
				i += 2
			case strings.HasPrefix(v, "--git-dir=") || strings.HasPrefix(v, "--work-tree="):
				_, path, _ := strings.Cut(v, "=")
				roots = append(roots, path)
				i++
			default:
				return ExecOther, cmd
			}
		}
		if i >= len(f) {
			return ExecOther, cmd
		}
		inner := append([]string{f[0]}, f[i:]...)
		c, w := classifyArgvDepth(inner, cmd, workdir, depth+1)
		if outside(roots, workdir) != "" && rank[c] < rank[OutsideWorktree] {
			return OutsideWorktree, cmd
		}
		return c, w
	}
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
	// sed -i and sort -o look read-only but write the files they name.
	writes := name == "sed" && (has("-i", "--in-place") || hasPrefixFlag(args, "-i")) || name == "sort" && has("-o", "--output")
	for _, a := range args {
		if filepath.IsAbs(a) || strings.HasPrefix(a, "~/") {
			if out := outside([]string{a}, workdir); out != "" && (!readOnly[name] || writes) && !isSystemPath(a) {
				return OutsideWorktree, cmd
			}
		} else if !readOnly[name] && strings.Contains(a, "..") && !strings.HasPrefix(a, "-") {
			if out := outside([]string{a}, workdir); out != "" {
				return OutsideWorktree, cmd // a relative path that climbs out
			}
		}
	}
	switch {
	case name == "find" && has("-delete"):
		return Delete, cmd
	case name == "find" && (has("-exec", "-execdir", "-ok", "-okdir")):
		for i, a := range args {
			if a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" {
				var inner []string
				for _, x := range args[i+1:] {
					if x == ";" || x == "\\;" || x == "+" {
						break
					}
					inner = append(inner, x)
				}
				if c, _ := classifyOneDepth(strings.Join(inner, " "), workdir, depth+1); rank[c] > rank[ExecOther] {
					return c, cmd
				}
				return ExecOther, cmd
			}
		}
	case (name == "awk" || name == "gawk") && strings.Contains(cmd, "system("):
		return ExecOther, cmd
	case writes:
		return EditWorktree, ""
	}

	if len(args) == 1 && (sub == "--version" || sub == "-V") && !strings.Contains(cmd, ">") {
		return Read, "" // a tool's version
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
			if sub == "remote" && len(args) > 1 && args[1] != "-v" && args[1] != "--verbose" && args[1] != "show" && args[1] != "get-url" {
				return ExecOther, cmd // add, set-url, remove: changes where pushes go
			}
			if sub == "config" && !has("--get", "--get-all", "--get-regexp", "--list", "-l", "--show-origin", "--get-urlmatch") && positional(args[1:]) > 1 {
				return ExecOther, cmd // setting a value
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
	case "terraform", "tofu":
		switch sub {
		case "validate", "fmt", "version", "output", "show", "graph", "providers":
			return ExecSafe, ""
		case "apply":
			return Service, cmd
		case "destroy":
			return Irreversible, cmd
		}
		return ExecOther, cmd
	case "kubectl":
		switch sub {
		case "get", "describe", "logs", "top", "explain", "version", "api-resources", "diff", "events":
			return Read, ""
		case "delete", "drain":
			return Irreversible, cmd
		case "apply", "rollout", "scale", "patch", "annotate", "label", "create", "replace", "set", "expose", "run", "exec", "cp", "edit", "port-forward", "cordon", "uncordon", "taint":
			return Service, cmd
		}
		return ExecOther, cmd
	case "helm":
		switch sub {
		case "list", "ls", "status", "get", "show", "template", "lint", "search", "version", "history":
			return Read, ""
		case "uninstall", "delete":
			return Irreversible, cmd
		case "install", "upgrade", "rollback", "repo", "plugin":
			return Service, cmd
		}
		return ExecOther, cmd
	case "python", "python3", "py":
		if sub == "-m" && len(args) > 1 {
			switch args[1] {
			case "pytest", "unittest", "compileall", "py_compile", "mypy", "ruff", "black", "flake8", "isort", "pylint", "coverage":
				return ExecSafe, ""
			case "pip":
				if len(args) > 2 && (args[2] == "list" || args[2] == "show" || args[2] == "freeze") {
					return Read, ""
				}
				return Install, cmd
			}
		}
		return ExecOther, cmd
	case "node":
		if has("--test") {
			return ExecSafe, ""
		}
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

// isSystemPath is for files that belong to the system and are safe to read
// (headers, tools' config samples). Temporary directories are not among
// them: they belong to other processes and projects, and on Linux a test or
// workspace often lives under /tmp, which must still count as outside the
// workdir.
func isSystemPath(p string) bool {
	for _, prefix := range []string{"/usr/", "/bin/", "/opt/homebrew/", "/dev/null", "/etc/"} {
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
	// Looking at the machine (decision 0024: the supervisor's machine_look).
	"uptime": true, "free": true, "vm_stat": true, "sw_vers": true, "id": true, "lsof": true, "netstat": true, "ss": true,
	"nproc": true, "arch": true, "nvidia-smi": true,
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

// isWrapper reports whether a command name is one of the wrappers.
func isWrapper(name string) bool {
	_, ok := wrappers[filepath.Base(name)]
	return ok
}

// hasPrefixFlag reports whether an argument starts with the short flag, as
// in sed -i.bak.
func hasPrefixFlag(args []string, flag string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, flag) && !strings.HasPrefix(a, "--") {
			return true
		}
	}
	return false
}

// positional counts the arguments that are not flags.
func positional(args []string) int {
	n := 0
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			n++
		}
	}
	return n
}

// substitutions splits the command substitutions out of a command line. It
// returns the line with each one replaced by a word, the commands that were
// inside, and whether one could not be closed. Single-quoted text is left
// alone, since the shell does not expand it.
func substitutions(cmd string) (string, []string, bool) {
	if !strings.ContainsAny(cmd, "$`<>") {
		return cmd, nil, false
	}
	var out strings.Builder
	var subs []string
	open := false
	inSingle, inDouble := false, false
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case inSingle:
			out.WriteRune(c)
			inSingle = c != '\''
		case c == '\\' && i+1 < len(rs):
			out.WriteRune(c)
			out.WriteRune(rs[i+1])
			i++
		case c == '\'' && !inDouble:
			inSingle = true
			out.WriteRune(c)
		case c == '"':
			inDouble = !inDouble
			out.WriteRune(c)
		case c == '`':
			j := i + 1
			for j < len(rs) && rs[j] != '`' {
				j++
			}
			if j >= len(rs) {
				open = true
				out.WriteRune(c)
				continue
			}
			subs = append(subs, string(rs[i+1:j]))
			out.WriteString("SUB")
			i = j
		case (c == '$' || (!inDouble && (c == '<' || c == '>'))) && i+1 < len(rs) && rs[i+1] == '(' && !(c == '$' && i+2 < len(rs) && rs[i+2] == '('):
			depth, j := 1, i+2
			for ; j < len(rs) && depth > 0; j++ {
				switch rs[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			if depth > 0 {
				open = true
				out.WriteRune(c)
				continue
			}
			subs = append(subs, string(rs[i+2:j-1]))
			out.WriteString("SUB")
			i = j - 1
		default:
			out.WriteRune(c)
		}
	}
	return out.String(), subs, open
}

// ResolveRequest preserves execution context supplied by the adapter while
// keeping Workdir as the node-owned trust root.
func ResolveRequest(r Request) Request {
	in := parseInput(r.RawInput)
	if in.cwd != "" {
		r.CWD = in.cwd
		if !filepath.IsAbs(r.CWD) && !strings.HasPrefix(r.CWD, "~") {
			r.CWD = filepath.Join(r.Workdir, r.CWD)
		}
	}
	if in.shell != "" {
		r.Shell = in.shell
	}
	return r
}

func (p *Policy) ShellEnabled() bool { return !p.legacyShell }

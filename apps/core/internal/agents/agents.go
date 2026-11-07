// Package agents knows the coding agents a node can run: how to find them,
// how to tell whether their user is logged in, and how to start their ACP
// server. Results of checking each agent on real machines are recorded in
// work/specs/sous-mvp/design.md.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Spec describes one agent.
type Spec struct {
	Name  string
	Title string
	// Binary is looked up on PATH to decide whether the agent is installed.
	Binary      string
	VersionArgs []string
	// Launch starts the agent's ACP server. The first element is looked up on PATH.
	Launch []string
	// Env is added to the agent's environment.
	Env []string
	// LoginStatus runs a command and reads its output; nil means unknown.
	LoginArgs []string
	LoggedIn  func(out string, err error) *bool
	Note      string
	// AskMode and OpenMode are the session modes to switch to for the
	// default/auto and the open permission modes, when the agent's names
	// differ from the usual ones (see node.pickMode). Empty: guess.
	AskMode  string
	OpenMode string
	// AskOptions are session config options sous sets itself in the
	// default and auto permission modes, so the agent asks before acting
	// (Kiro's autopilot); OpenOptions in the open mode. The user cannot
	// pick defaults for them.
	AskOptions  map[string]string
	OpenOptions map[string]string

	// Remote administration (admin.go). Install and Upgrade are keyed by
	// GOOS, "unix" (darwin and linux) or "*"; an empty Upgrade reuses
	// Install. Every command is a constant here: requests name the agent,
	// never a command.
	Install map[string]Recipe
	Upgrade map[string]Recipe
	Login   map[string]Recipe
	Logout  map[string]Recipe
	// Requires lists other commands the agent needs on PATH.
	Requires []string
	// Configs are the agent's own config files the user may edit.
	Configs []proto.AgentConfigFile
	// KeyEnv are environment variables that log the agent in with an API key.
	KeyEnv []string
	// Custom marks an agent from node.json's custom_agents.
	Custom bool
}

var (
	yes = func() *bool { b := true; return &b }
	no  = func() *bool { b := false; return &b }
)

// Pinned versions of the ACP adapters for agents without a native ACP server.
const (
	ClaudeACP = "@agentclientprotocol/claude-agent-acp@0.100.0"
	CodexACP  = "@agentclientprotocol/codex-acp@2.5.0"
)

// Builtin lists the agents sous supports out of the box.
func Builtin() []Spec {
	return []Spec{
		{
			Name: "claude", Title: "Claude Code", Binary: "claude", VersionArgs: []string{"--version"},
			Launch:    []string{"npx", "-y", ClaudeACP},
			LoginArgs: []string{"auth", "status"},
			LoggedIn: func(out string, err error) *bool {
				var s struct {
					LoggedIn *bool `json:"loggedIn"`
				}
				if json.Unmarshal([]byte(out), &s) == nil && s.LoggedIn != nil {
					return s.LoggedIn
				}
				return nil
			},
			Note: "通过 ACP 适配器运行，需要 Node.js（npx）",
			Install: map[string]Recipe{
				"unix":    {Script: "curl -fsSL https://claude.ai/install.sh | bash"},
				"windows": {Script: "irm https://claude.ai/install.ps1 | iex"},
			},
			Upgrade: map[string]Recipe{"*": {Argv: []string{"claude", "update"}}},
			// The page it links to shows a code to paste back into the terminal.
			Login:    map[string]Recipe{"*": {Argv: []string{"claude", "auth", "login"}}},
			Logout:   map[string]Recipe{"*": {Argv: []string{"claude", "auth", "logout"}}},
			Requires: []string{"npx"},
			Configs:  []proto.AgentConfigFile{{ID: "settings", Path: "~/.claude/settings.json", Format: "json"}},
			KeyEnv:   []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
		},
		{
			Name: "codex", Title: "Codex", Binary: "codex", VersionArgs: []string{"--version"},
			Launch:    []string{"npx", "-y", CodexACP},
			LoginArgs: []string{"login", "status"},
			LoggedIn: func(out string, err error) *bool {
				if strings.Contains(out, "Logged in") && !strings.Contains(out, "Not logged in") {
					return yes()
				}
				// `codex login status` ignores providers configured in
				// config.toml with their own key (codex works without login).
				if ok := codexConfigAuth(codexHome(), os.Getenv); ok != nil {
					return ok
				}
				if strings.Contains(out, "Not logged in") {
					return no()
				}
				return nil
			},
			Note:     "通过 ACP 适配器运行，需要 Node.js（npx）",
			Install:  map[string]Recipe{"*": {Argv: []string{"npm", "install", "-g", "@openai/codex"}}},
			Login:    map[string]Recipe{"*": {Argv: []string{"codex", "login", "--device-auth"}}},
			Logout:   map[string]Recipe{"*": {Argv: []string{"codex", "logout"}}},
			Requires: []string{"npx"},
			Configs:  []proto.AgentConfigFile{{ID: "config", Path: "~/.codex/config.toml", Format: "toml"}},
			KeyEnv:   []string{"OPENAI_API_KEY"},
		},
		{
			Name: "cursor", Title: "Cursor", Binary: "cursor-agent", VersionArgs: []string{"--version"},
			Launch: []string{"cursor-agent", "acp"},
			// Cursor's modes are agent, plan and ask; its "ask" is a
			// read-only Q&A mode, so it must stay in "agent". In agent mode
			// it asks before shell commands (verified 2026-11-04).
			AskMode: "agent", OpenMode: "agent",
			LoginArgs: []string{"status"},
			LoggedIn: func(out string, err error) *bool {
				switch {
				case strings.Contains(out, "Not logged in"):
					return no()
				case strings.Contains(out, "Logged in"):
					return yes()
				}
				return nil
			},
			Install: map[string]Recipe{
				"unix":    {Script: "curl https://cursor.com/install -fsS | bash"},
				"windows": {Script: "irm 'https://cursor.com/install?win32=true' | iex"},
			},
			Upgrade: map[string]Recipe{"*": {Argv: []string{"cursor-agent", "update"}}},
			Login:   map[string]Recipe{"*": {Argv: []string{"cursor-agent", "login"}, Env: []string{"NO_OPEN_BROWSER=1"}}},
			Logout:  map[string]Recipe{"*": {Argv: []string{"cursor-agent", "logout"}}},
			Configs: []proto.AgentConfigFile{{ID: "cli-config", Path: "~/.cursor/cli-config.json", Format: "json"}},
			KeyEnv:  []string{"CURSOR_API_KEY"},
		},
		{
			Name: "opencode", Title: "OpenCode", Binary: "opencode", VersionArgs: []string{"--version"},
			Launch: []string{"opencode", "acp"},
			// opencode has no "ask" session mode over ACP and allows edits and
			// shell commands by default, which would bypass sous's policy. This
			// inline config (merged over the user's own) makes it ask instead.
			// Pick a model in opencode.json or with OPENCODE_CONFIG=<file>;
			// overriding OPENCODE_CONFIG_CONTENT would drop these rules.
			Env:  []string{`OPENCODE_CONFIG_CONTENT={"permission":{"edit":"ask","bash":"ask","webfetch":"ask","external_directory":"ask"}}`},
			Note: "可以使用多个模型服务商，登录状态无法预先判断",
			Install: map[string]Recipe{
				"unix":    {Script: "curl -fsSL https://opencode.ai/install | bash"},
				"windows": {Argv: []string{"npm", "install", "-g", "opencode-ai"}},
			},
			Upgrade: map[string]Recipe{"*": {Argv: []string{"opencode", "upgrade"}}},
			// An interactive wizard: pick a provider, then paste its key.
			Login:   map[string]Recipe{"*": {Argv: []string{"opencode", "auth", "login"}}},
			Logout:  map[string]Recipe{"*": {Argv: []string{"opencode", "auth", "logout"}}},
			Configs: []proto.AgentConfigFile{{ID: "config", Path: "~/.config/opencode/opencode.json", Format: "jsonc"}},
		},
		{
			Name: "kiro", Title: "Kiro", Binary: "kiro-cli", VersionArgs: []string{"--version"},
			Launch: []string{"kiro-cli", "acp"},
			// Its "autopilot" option (on by default) runs shell commands
			// without asking, which would bypass sous's policy (verified
			// 2026-11-04 with kiro-cli 3.0.0). Its modes (vibe, spec, plan…)
			// are workflows, not permission levels.
			AskOptions:  map[string]string{"autopilot": "off"},
			OpenOptions: map[string]string{"autopilot": "on"},
			LoginArgs:   []string{"whoami"},
			LoggedIn: func(out string, err error) *bool {
				switch {
				case strings.Contains(strings.ToLower(out), "not logged in"):
					return no()
				case err == nil && strings.TrimSpace(out) != "":
					return yes()
				}
				return nil
			},
			Install: map[string]Recipe{"unix": {Script: "curl -fsSL https://cli.kiro.dev/install | bash"}},
			Upgrade: map[string]Recipe{"*": {Argv: []string{"kiro-cli", "update"}}},
			Login:   map[string]Recipe{"*": {Argv: []string{"kiro-cli", "login", "--use-device-flow"}}},
			Logout:  map[string]Recipe{"*": {Argv: []string{"kiro-cli", "logout"}}},
			// Kiro also obeys its own permissions.yaml: what a rule allows runs
			// without asking sous, even with autopilot off.
			Note: "Kiro 会按 ~/.kiro/settings/permissions.yaml 里的规则自己放行操作，这些操作不经过 sous 的审批",
			Configs: []proto.AgentConfigFile{
				{ID: "permissions", Path: "~/.kiro/settings/permissions.yaml", Format: "yaml"},
				{ID: "settings", Path: "~/.kiro/settings/cli.json", Format: "json"},
				{ID: "mcp", Path: "~/.kiro/settings/mcp.json", Format: "json"},
			},
		},
	}
}

// Find returns the spec with the given name.
func Find(specs []Spec, name string) (Spec, bool) {
	for _, s := range specs {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// Command returns how to start the agent's ACP server in dir.
func (s Spec) Command(dir string) (acp.Command, error) {
	path, err := exec.LookPath(s.Launch[0])
	if err != nil {
		return acp.Command{}, err
	}
	return acp.Command{Path: path, Args: s.Launch[1:], Dir: dir, Env: s.Env}, nil
}

const probeTimeout = 15 * time.Second

// Detect checks every agent in parallel.
func Detect(ctx context.Context, specs []Spec) []proto.AgentInfo {
	out := make([]proto.AgentInfo, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = detect(ctx, s)
		}()
	}
	wg.Wait()
	return out
}

func detect(ctx context.Context, s Spec) proto.AgentInfo {
	info := proto.AgentInfo{Name: s.Name, Title: s.Title, Transport: "acp", Note: s.Note,
		KeyEnv: s.KeyEnv, Configs: s.Configs, Custom: s.Custom}
	if r, ok := pick(s.Install); ok {
		info.Install = r.String()
	}
	for _, need := range s.Requires {
		if _, err := exec.LookPath(need); err != nil {
			info.Missing = append(info.Missing, need)
		}
	}
	path, err := exec.LookPath(s.Binary)
	if err != nil {
		return info
	}
	info.Installed = true
	if r, ok := s.UpgradeRecipe(); ok {
		info.Upgrade = r.String()
	}
	if r, ok := pick(s.Login); ok {
		info.Login = r.String()
	}
	if r, ok := pick(s.Logout); ok {
		info.Logout = r.String()
	}
	if len(s.VersionArgs) > 0 {
		if out, err := run(ctx, s.Env, path, s.VersionArgs...); err == nil {
			info.Version = firstLine(out)
		}
	}
	if s.LoggedIn != nil && len(s.LoginArgs) > 0 {
		out, err := run(ctx, s.Env, path, s.LoginArgs...)
		info.LoggedIn = s.LoggedIn(out, err)
	}
	if _, err := exec.LookPath(s.Launch[0]); err != nil {
		info.Note = strings.TrimSpace(info.Note + "；找不到 " + s.Launch[0] + "，无法启动")
		info.LoggedIn = no()
	}
	return info
}

func run(ctx context.Context, env []string, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var b bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := cmd.Run()
	return b.String(), err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

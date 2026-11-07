// Package node is the daemon that runs on each of the user's machines. It
// connects to the Hub, reports which coding agents it has, and runs agents
// in worktrees when the Hub asks, relaying progress and approval requests.
package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
)

// Config is ~/.sous/node.json. It holds the device token, so it is written
// with mode 0600.
type Config struct {
	HubURL string `json:"hub_url"` // https://agent.wxian.cc
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Token  string `json:"token"`
	// Concurrency bounds how many runs execute at once (default 2).
	Concurrency int `json:"concurrency,omitempty"`
	// SousMCP overrides how sous's own MCP server for sub-agents starts
	// (decision 0025; default: this executable with "mcp"); ["off"] turns
	// it off. It decides what runs here, so only this file sets it.
	SousMCP []string `json:"sous_mcp,omitempty"`
	// Agents overrides how an agent's ACP server starts, by agent name:
	// {"claude": ["/path/to/claude-agent-acp"]}.
	Agents map[string][]string `json:"agents,omitempty"`
	// Policy holds the floor rules (design §8); they apply in every mode.
	Policy policy.Config `json:"policy,omitempty"`
	// AgentOptions are the session config options set on every new
	// session of an agent: {"claude": {"model": "opus"}}.
	AgentOptions map[string]map[string]string `json:"agent_options,omitempty"`
	// AgentEnv is added to an agent's environment, such as an API key:
	// {"claude": {"ANTHROPIC_API_KEY": "..."}}.
	AgentEnv map[string]map[string]string `json:"agent_env,omitempty"`
	// RemoteAdmin false refuses remote administration from the Hub
	// (installing agents, logging in, editing config files). Only this
	// file can turn it off or on again. Default on.
	RemoteAdmin *bool `json:"remote_admin,omitempty"`
	// RemoteShell false refuses shells opened from the page (a terminal in
	// a project's directory). Needs remote administration too. Only this
	// file can change it. Default on.
	RemoteShell *bool `json:"remote_shell,omitempty"`
	// AutoFetch false stops the background `git fetch` of the repositories
	// in the workspaces (every 10 minutes, so the page can say a checkout
	// is behind). It never changes a checkout. Default on.
	AutoFetch *bool `json:"auto_fetch,omitempty"`
	// ConsultIdleMinutes is how long a project consultant waits for the
	// next question before its agent stops (default 30); the session is
	// kept and loaded again for the next question (decision 0022).
	ConsultIdleMinutes int `json:"consult_idle_minutes,omitempty"`
	// Role is "dev" (default) or "server" (M5, docs/decisions/0014). The
	// Hub may turn a dev machine into a server, never back: only this file
	// can.
	Role string `json:"role,omitempty"`
	// Workspaces are the directories searched for git repositories; new
	// projects are cloned or created in the first one. "~/" is expanded.
	Workspaces []string `json:"workspaces,omitempty"`
	// DefaultAgent is this machine's default agent: used when neither the
	// task, its preset nor its project names one (decision 0024).
	DefaultAgent string `json:"default_agent,omitempty"`
	// DisabledAgents are agents this machine does not run, installed or not.
	DisabledAgents []string `json:"disabled_agents,omitempty"`
	// CustomAgents are more ACP agents. They decide what the machine runs,
	// so only an edit on this machine changes them; the Hub cannot.
	CustomAgents []CustomAgent `json:"custom_agents,omitempty"`
}

// CustomAgent is an ACP agent beyond the built-in ones.
type CustomAgent struct {
	// Name is how sous refers to it: lowercase letters, digits and "-",
	// different from the built-in agents.
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	// Command starts its ACP server; the first element is looked up on PATH.
	Command []string `json:"command"`
}

// AgentDisabled reports whether node.json turns the agent off.
func (c Config) AgentDisabled(name string) bool {
	for _, n := range c.DisabledAgents {
		if n == name {
			return true
		}
	}
	return false
}

// RoleName is the machine's role, dev unless set to server.
func (c Config) RoleName() string {
	if c.Role == "server" {
		return "server"
	}
	return "dev"
}

// WorkspaceDirs returns the workspaces as absolute paths.
func (c Config) WorkspaceDirs() []string {
	out := []string{}
	for _, w := range c.Workspaces {
		if p := ExpandPath(w); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ExpandPath expands a leading "~" and makes the path absolute.
func ExpandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// AdminEnabled reports whether the Hub may administer this machine.
func (c Config) AdminEnabled() bool { return c.RemoteAdmin == nil || *c.RemoteAdmin }

// ShellEnabled reports whether the page may open shells on this machine.
func (c Config) ShellEnabled() bool {
	return c.AdminEnabled() && (c.RemoteShell == nil || *c.RemoteShell)
}

// Root is ~/.sous, where the config, worktrees and scratch directories live.
// SOUS_HOME overrides it.
func Root() (string, error) {
	if d := os.Getenv("SOUS_HOME"); d != "" {
		// Agents insist on absolute working directories, which live here.
		return filepath.Abs(d)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sous"), nil
}

func configPath(root string) string { return filepath.Join(root, "node.json") }

var ErrNotPaired = errors.New("this machine is not paired yet: run `sous pair <hub-url> <code>`")

// LoadConfig reads the config from root.
func LoadConfig(root string) (Config, error) {
	raw, err := os.ReadFile(configPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotPaired
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", configPath(root), err)
	}
	if c.HubURL == "" || c.NodeID == "" || c.Token == "" {
		return Config{}, ErrNotPaired
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 2
	}
	return c, nil
}

// SaveConfig writes the config to root with mode 0600.
func SaveConfig(root string, c Config) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath(root) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, configPath(root))
}

// NormalizeHubURL accepts "agent.wxian.cc" or a full URL and returns its origin.
func NormalizeHubURL(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("invalid hub url %q", s)
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return "", fmt.Errorf("hub url must use https outside localhost")
	}
	return u.Scheme + "://" + u.Host, nil
}

// wsURL turns the hub origin into its /node WebSocket URL.
func wsURL(hub string) string {
	if strings.HasPrefix(hub, "https://") {
		return "wss://" + strings.TrimPrefix(hub, "https://") + "/node"
	}
	return "ws://" + strings.TrimPrefix(hub, "http://") + "/node"
}

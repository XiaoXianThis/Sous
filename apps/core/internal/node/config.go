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
	// Agents overrides how an agent's ACP server starts, by agent name:
	// {"claude": ["/path/to/claude-agent-acp"]}.
	Agents map[string][]string `json:"agents,omitempty"`
}

// Root is ~/.sous, where the config, worktrees and scratch directories live.
// SOUS_HOME overrides it.
func Root() (string, error) {
	if d := os.Getenv("SOUS_HOME"); d != "" {
		return d, nil
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

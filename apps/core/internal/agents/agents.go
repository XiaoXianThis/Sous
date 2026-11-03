// Package agents knows the coding agents a node can run: how to find them,
// how to tell whether their user is logged in, and how to start their ACP
// server. Results of checking each agent on real machines are recorded in
// work/specs/sous-mvp/design.md.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
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
		},
		{
			Name: "codex", Title: "Codex", Binary: "codex", VersionArgs: []string{"--version"},
			Launch:    []string{"npx", "-y", CodexACP},
			LoginArgs: []string{"login", "status"},
			LoggedIn: func(out string, err error) *bool {
				switch {
				case strings.Contains(out, "Not logged in"):
					return no()
				case strings.Contains(out, "Logged in"):
					return yes()
				}
				return nil
			},
			Note: "通过 ACP 适配器运行，需要 Node.js（npx）",
		},
		{
			Name: "cursor", Title: "Cursor", Binary: "cursor-agent", VersionArgs: []string{"--version"},
			Launch:    []string{"cursor-agent", "acp"},
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
		},
		{
			Name: "opencode", Title: "OpenCode", Binary: "opencode", VersionArgs: []string{"--version"},
			Launch: []string{"opencode", "acp"},
			Note:   "可以使用多个模型服务商，登录状态无法预先判断",
		},
		{
			Name: "kiro", Title: "Kiro", Binary: "kiro-cli", VersionArgs: []string{"--version"},
			Launch: []string{"kiro-cli", "acp"},
			Note:   "尚未在真实环境中验证",
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
	info := proto.AgentInfo{Name: s.Name, Title: s.Title, Transport: "acp", Note: s.Note}
	path, err := exec.LookPath(s.Binary)
	if err != nil {
		return info
	}
	info.Installed = true
	if len(s.VersionArgs) > 0 {
		if out, err := run(ctx, path, s.VersionArgs...); err == nil {
			info.Version = firstLine(out)
		}
	}
	if s.LoggedIn != nil && len(s.LoginArgs) > 0 {
		out, err := run(ctx, path, s.LoginArgs...)
		info.LoggedIn = s.LoggedIn(out, err)
	}
	if _, err := exec.LookPath(s.Launch[0]); err != nil {
		info.Note = strings.TrimSpace(info.Note + "；找不到 " + s.Launch[0] + "，无法启动")
		info.LoggedIn = no()
	}
	return info
}

func run(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var b bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
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

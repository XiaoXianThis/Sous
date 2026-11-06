package agents

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// codexProvider is the part of a model provider in ~/.codex/config.toml
// that tells whether it needs `codex login`.
type codexProvider struct {
	EnvKey             string `toml:"env_key"`
	BearerToken        string `toml:"experimental_bearer_token"`
	RequiresOpenAIAuth bool   `toml:"requires_openai_auth"`
}

type codexConfig struct {
	ModelProvider  string                   `toml:"model_provider"`
	Profile        string                   `toml:"profile"`
	ModelProviders map[string]codexProvider `toml:"model_providers"`
	Profiles       map[string]struct {
		ModelProvider string `toml:"model_provider"`
	} `toml:"profiles"`
}

// codexHome is where codex keeps its config: $CODEX_HOME or ~/.codex.
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexConfigAuth tells whether codex can run without `codex login`
// because its config.toml selects a model provider that brings its own
// credentials. `codex login status` only knows about auth.json, so it says
// "Not logged in" for such setups even though codex works. It returns nil
// when the config does not settle it.
func codexConfigAuth(home string, getenv func(string) string) *bool {
	if home == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return nil
	}
	var c codexConfig
	if toml.Unmarshal(data, &c) != nil {
		return nil
	}
	name := c.ModelProvider
	if p, ok := c.Profiles[c.Profile]; ok && c.Profile != "" && p.ModelProvider != "" {
		name = p.ModelProvider
	}
	switch name {
	case "", "openai":
		// The built-in provider uses `codex login` or OPENAI_API_KEY.
		return nil
	case "ollama", "lmstudio", "oss":
		// Local providers need no credentials.
		return yes()
	}
	p, ok := c.ModelProviders[name]
	if !ok || p.RequiresOpenAIAuth {
		return nil
	}
	switch {
	case strings.TrimSpace(p.BearerToken) != "":
		return yes()
	case p.EnvKey != "":
		if getenv(p.EnvKey) != "" {
			return yes()
		}
		// The key may still come from node.json's agent_env.
		return nil
	}
	// A provider without any credentials (a local or open endpoint).
	return yes()
}

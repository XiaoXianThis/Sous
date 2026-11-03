// Package config loads hub.yaml. Secrets never live in the file: a provider
// names the environment variable that holds its API key.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Hub struct {
	// Listen is the address the HTTP server binds, e.g. 127.0.0.1:8787.
	Listen string `yaml:"listen"`
	// PublicURL is the origin users open, e.g. https://agent.wxian.cc. It is
	// also the WebAuthn origin, and its host is the relying party ID.
	PublicURL string `yaml:"public_url"`
	// DataDir holds hub.db and run logs. Relative paths resolve against the
	// config file's directory.
	DataDir string `yaml:"data_dir"`
	// WebDir is the built PWA to serve (build/web). Empty disables it.
	WebDir string `yaml:"web_dir"`
	// Dev enables the loopback-only dev login and relaxes cookie security.
	Dev bool `yaml:"dev"`
	// EnvFile is an optional KEY=VALUE file (never committed) that sets
	// environment variables, such as API keys, before providers read them.
	// Variables already set in the environment win.
	EnvFile string `yaml:"env_file"`

	Providers []Provider        `yaml:"providers"`
	Roles     map[string]string `yaml:"roles"` // role -> "provider/model"
}

type Provider struct {
	Name      string `yaml:"name"`
	Kind      string `yaml:"kind"` // openai-chat / openai-responses / anthropic-messages / mock
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// APIKey reads the provider's key from its environment variable.
func (p Provider) APIKey() string {
	if p.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(p.APIKeyEnv)
}

// Roles the Hub uses.
const (
	RoleSupervisor = "supervisor"
	RoleReviewer   = "reviewer"
	RoleApprover   = "approver"
	RoleInspector  = "inspector"
)

// Load reads and validates a config file.
func Load(path string) (*Hub, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Hub{}
	if err := yaml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	base := filepath.Dir(path)
	c.applyDefaults(base)
	if c.EnvFile != "" {
		if err := loadEnvFile(c.EnvFile); err != nil {
			return nil, fmt.Errorf("%s: env_file: %w", path, err)
		}
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c *Hub) applyDefaults(base string) {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8787"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:8787"
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if !filepath.IsAbs(c.DataDir) {
		c.DataDir = filepath.Join(base, c.DataDir)
	}
	if c.WebDir != "" && !filepath.IsAbs(c.WebDir) {
		c.WebDir = filepath.Join(base, c.WebDir)
	}
	if c.EnvFile != "" && !filepath.IsAbs(c.EnvFile) {
		c.EnvFile = filepath.Join(base, c.EnvFile)
	}
	if c.Roles == nil {
		c.Roles = map[string]string{}
	}
	// Unset roles fall back to the supervisor's model.
	if sup, ok := c.Roles[RoleSupervisor]; ok {
		for _, r := range []string{RoleReviewer, RoleApprover, RoleInspector} {
			if c.Roles[r] == "" {
				c.Roles[r] = sup
			}
		}
	}
}

func (c *Hub) Validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("public_url %q must be an http(s) origin", c.PublicURL)
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return fmt.Errorf("public_url must use https outside localhost")
	}
	names := map[string]bool{}
	for _, p := range c.Providers {
		if p.Name == "" || p.Kind == "" {
			return fmt.Errorf("every provider needs a name and a kind")
		}
		if names[p.Name] {
			return fmt.Errorf("duplicate provider %q", p.Name)
		}
		names[p.Name] = true
	}
	if c.Roles[RoleSupervisor] == "" {
		return fmt.Errorf("roles.supervisor is required")
	}
	for role, ref := range c.Roles {
		prov, model, ok := strings.Cut(ref, "/")
		if !ok || prov == "" || model == "" {
			return fmt.Errorf("roles.%s %q must look like provider/model", role, ref)
		}
		if !names[prov] {
			return fmt.Errorf("roles.%s uses unknown provider %q", role, prov)
		}
	}
	return nil
}

// Model splits a role's "provider/model" reference.
func (c *Hub) Model(role string) (provider, model string) {
	provider, model, _ = strings.Cut(c.Roles[role], "/")
	return provider, model
}

// RPID is the WebAuthn relying party ID: the public URL's host name.
func (c *Hub) RPID() string {
	u, _ := url.Parse(c.PublicURL)
	return u.Hostname()
}

// Secure reports whether cookies should be marked Secure.
func (c *Hub) Secure() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

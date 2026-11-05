// Package config loads hub.yaml. Secrets never live in the file: a provider
// names the environment variable that holds its API key.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/XiaoXianThis/Sous/apps/core/internal/web"
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
	// TLSCert and TLSKey make the Hub serve HTTPS itself (PEM files),
	// for when no reverse proxy terminates TLS in front of it.
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
	// InsecureListen allows plain HTTP on an address other than loopback,
	// for a proxy on another host or a container network that encrypts
	// the hop itself. Without it such a listen address is refused.
	InsecureListen bool `yaml:"insecure_listen"`
	// Dev enables the loopback-only dev login and relaxes cookie security.
	Dev bool `yaml:"dev"`
	// EnvFile is an optional KEY=VALUE file (never committed) that sets
	// environment variables, such as API keys, before providers read them.
	// Variables already set in the environment win.
	EnvFile string `yaml:"env_file"`

	Providers []Provider        `yaml:"providers"`
	Roles     map[string]string `yaml:"roles"` // role -> "provider/model"
	// Efforts sets a role's reasoning effort (low / medium / high / xhigh
	// / max); a role without one uses the model's default.
	Efforts map[string]string `yaml:"efforts"`

	// M4: autonomy and long-running operation. Zero values take defaults.
	Triggers  Triggers  `yaml:"triggers"`
	Inspector Inspector `yaml:"inspector"`
	// RetentionDays: logs and diffs of tasks closed longer ago are
	// archived (default 90; -1 turns it off).
	RetentionDays int `yaml:"retention_days"`
	// UpdateRepo is the GitHub repository releases come from (owner/name).
	UpdateRepo string `yaml:"update_repo"`
	// Web gives the supervisor web_search and web_fetch.
	Web Web `yaml:"web"`

	explicit map[string]bool // roles the file sets itself
}

// RoleSet reports whether the file names a model for the role itself
// (rather than falling back on the supervisor's).
func (c *Hub) RoleSet(role string) bool { return c.explicit[role] }

// Triggers tunes follow-ups (design §9).
type Triggers struct {
	FollowupEvery time.Duration `yaml:"followup_every"` // default 5m
	BlockedAfter  time.Duration `yaml:"blocked_after"`  // default 4h
	RunTimeout    time.Duration `yaml:"run_timeout"`    // default 2h
	ApprovalAfter time.Duration `yaml:"approval_after"` // default 2h
	QuestionAfter time.Duration `yaml:"question_after"` // default 8h
}

// Inspector tunes the inspector agent (docs/decisions/0006).
type Inspector struct {
	Every        time.Duration `yaml:"every"`         // default 10m
	Repeat       int           `yaml:"repeat"`        // default 5
	StalledAfter time.Duration `yaml:"stalled_after"` // default 30m
	LongAfter    time.Duration `yaml:"long_after"`    // default 3h
	TokenAlarm   int           `yaml:"token_alarm"`   // default 2000000 per period
}

// Web configures the supervisor's internet tools (docs/decisions/0017).
type Web struct {
	// Disabled removes web_search and web_fetch.
	Disabled bool      `yaml:"disabled"`
	Search   WebSearch `yaml:"search"`
}

// WebSearch picks the search engine. Like model providers, it names the
// environment variable that holds the key instead of holding it.
type WebSearch struct {
	// Provider is duckduckgo (default, no key), brave, tavily or searxng.
	Provider  string `yaml:"provider"`
	APIKeyEnv string `yaml:"api_key_env"`
	// BaseURL is the SearXNG instance, e.g. https://searx.example.org.
	BaseURL string `yaml:"base_url"`
}

// APIKey reads the search key from its environment variable.
func (w WebSearch) APIKey() string {
	if w.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(w.APIKeyEnv)
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
	for _, f := range []*string{&c.TLSCert, &c.TLSKey} {
		if *f != "" && !filepath.IsAbs(*f) {
			*f = filepath.Join(base, *f)
		}
	}
	if c.EnvFile != "" && !filepath.IsAbs(c.EnvFile) {
		c.EnvFile = filepath.Join(base, c.EnvFile)
	}
	if c.Roles == nil {
		c.Roles = map[string]string{}
	}
	c.explicit = map[string]bool{}
	for r := range c.Roles {
		c.explicit[r] = true
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
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return fmt.Errorf("tls_cert and tls_key go together")
	}
	if c.TLSCert == "" && !c.InsecureListen && !loopbackListen(c.Listen) {
		return fmt.Errorf("listen %q is reachable from other machines over plain HTTP: listen on 127.0.0.1 behind a TLS proxy, set tls_cert and tls_key, or set insecure_listen: true if the network in between is encrypted", c.Listen)
	}
	if p := c.Web.Search.Provider; p != "" && !web.Known(p) {
		return fmt.Errorf("web.search.provider %q must be duckduckgo, brave, tavily or searxng", p)
	}
	if c.Web.Search.Provider == web.SearXNG {
		if u, err := url.Parse(c.Web.Search.BaseURL); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return fmt.Errorf("web.search.base_url %q must be the http(s) address of a SearXNG instance", c.Web.Search.BaseURL)
		}
	}
	if web.NeedsKey(c.Web.Search.Provider) && c.Web.Search.APIKeyEnv == "" {
		return fmt.Errorf("web.search.provider %s needs api_key_env", c.Web.Search.Provider)
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
	// Roles are optional: models are configured on the page (stored in the
	// database); the file's providers and roles are imported once.
	for role, e := range c.Efforts {
		if _, ok := c.Roles[role]; !ok {
			return fmt.Errorf("efforts.%s: no such role", role)
		}
		if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, e) {
			return fmt.Errorf("efforts.%s %q must be low, medium, high, xhigh or max", role, e)
		}
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

// loopbackListen reports whether addr only accepts connections from this
// machine.
func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// TLS reports whether the Hub serves HTTPS itself.
func (c *Hub) TLS() bool { return c.TLSCert != "" }

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

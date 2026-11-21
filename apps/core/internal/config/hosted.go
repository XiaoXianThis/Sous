package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Hosted is the operator-only configuration for one isolated workspace. It
// does not accept legacy provider/tool/Node settings or import a local home.
type Hosted struct {
	Listen                   string              `yaml:"listen"`
	PublicURL                string              `yaml:"public_url"`
	DataDir                  string              `yaml:"data_dir"`
	WebDir                   string              `yaml:"web_dir"`
	TLSCert                  string              `yaml:"tls_cert"`
	TLSKey                   string              `yaml:"tls_key"`
	AgentConfig              string              `yaml:"agent_config"`
	CloudWorkerTokenFile     string              `yaml:"cloud_worker_token_file"`
	CloudControllerTokenFile string              `yaml:"cloud_controller_token_file"`
	CloudIdleSeconds         *int                `yaml:"cloud_idle_seconds"`
	ExternalMCPClients       []ExternalMCPClient `yaml:"external_mcp_clients"`
}

func LoadHosted(name string) (*Hub, string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	var hosted Hosted
	if err := decodeConfig(f, &hosted); err != nil {
		return nil, "", fmt.Errorf("hosted workspace config: %w", err)
	}
	if hosted.DataDir == "" || hosted.WebDir == "" {
		return nil, "", errors.New("hosted workspace requires explicit data_dir and web_dir")
	}
	if hosted.Listen == "" {
		hosted.Listen = "127.0.0.1:8787"
	}
	base, err := filepath.Abs(filepath.Dir(name))
	if err != nil {
		return nil, "", err
	}
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	cfg := ForLocalWorkspace(abs(hosted.DataDir))
	cfg.LocalWorkspace, cfg.HostedWorkspace = false, true
	cfg.ExternalMCPClients = hosted.ExternalMCPClients
	cfg.Listen, cfg.PublicURL, cfg.WebDir = hosted.Listen, hosted.PublicURL, abs(hosted.WebDir)
	cfg.TLSCert, cfg.TLSKey = abs(hosted.TLSCert), abs(hosted.TLSKey)
	if hosted.CloudControllerTokenFile != "" {
		cfg.CloudControllerToken, err = ReadWorkerToken(abs(hosted.CloudControllerTokenFile))
		if err != nil {
			return nil, "", fmt.Errorf("cloud controller credential: %w", err)
		}
		cfg.CloudIdleTimeout = 5 * time.Minute
	}
	if hosted.CloudIdleSeconds != nil {
		if cfg.CloudControllerToken == "" || *hosted.CloudIdleSeconds < 5 || *hosted.CloudIdleSeconds > 3600 {
			return nil, "", errors.New("cloud_idle_seconds requires a controller and must be 5–3600")
		}
		cfg.CloudIdleTimeout = time.Duration(*hosted.CloudIdleSeconds) * time.Second
	}
	if hosted.CloudWorkerTokenFile != "" {
		cfg.CloudWorkerToken, err = ReadWorkerToken(abs(hosted.CloudWorkerTokenFile))
		if err != nil {
			return nil, "", err
		}
	}
	return cfg, abs(hosted.AgentConfig), cfg.Validate()
}

func (c *Hub) validateHosted(u *url.URL) error {
	if err := validateExternalClients(c.ExternalMCPClients); err != nil {
		return err
	}
	if c.CloudControllerToken != "" && c.CloudWorkerToken != "" {
		return errors.New("static and managed cloud worker modes are mutually exclusive")
	}

	if c.LocalWorkspace || c.LocalSandboxDispatch || c.Dev || c.InsecureListen || !c.Web.Disabled || !c.MCP.Disabled {
		return errors.New("hosted workspace cannot enable development login, local dispatch, or host tools")
	}
	if u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(c.PublicURL, "#") || u.Port() == "443" || u.Host != strings.ToLower(u.Host) {
		return errors.New("hosted public_url must be the exact canonical HTTPS origin without a path, query, fragment, credentials, or default port")
	}
	// Require a DNS identity to avoid ambiguous browser RP IDs and to make
	// the initial link usable from another device without a host rewrite.
	host := u.Hostname()
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return errors.New("hosted public_url requires a DNS name")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid hosted DNS name")
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return errors.New("hosted DNS name must use canonical ASCII labels")
			}
		}
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || u.Host != net.JoinHostPort(host, strconv.Itoa(port)) {
			return errors.New("invalid hosted public_url port")
		}
	} else if u.Host != host {
		return errors.New("hosted public_url is not canonical")
	}
	_, listenPort, err := net.SplitHostPort(c.Listen)
	p, perr := strconv.Atoi(listenPort)
	if err != nil || perr != nil || p < 1 || p > 65535 {
		return errors.New("hosted listen needs a fixed TCP port")
	}
	if c.DataDir == "" || c.WebDir == "" {
		return errors.New("hosted workspace requires explicit data_dir and web_dir")
	}
	return nil
}

func decodeConfig(r io.Reader, out any) error {
	const maximum = 16 << 10
	raw, err := io.ReadAll(io.LimitReader(r, maximum+1))
	if err != nil {
		return err
	}
	if len(raw) > maximum {
		return errors.New("config is too large")
	}
	d := yaml.NewDecoder(strings.NewReader(string(raw)))
	d.KnownFields(true)
	if err := d.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("expected one YAML document")
	}
	return nil
}

// ReadWorkerToken deliberately accepts only a private, regular token file.
func ReadWorkerToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 128 {
		return "", errors.New("cloud worker token must be in a private regular file (0600)")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return "", errors.New("cloud worker token file changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	if len(s) != 64 {
		return "", errors.New("cloud worker token must be 32 random bytes encoded as lowercase hex")
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", errors.New("invalid cloud worker token format")
		}
	}
	return s, nil
}

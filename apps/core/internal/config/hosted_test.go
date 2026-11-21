package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedConfigDeploymentBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, origin, overrides string
		valid                   bool
	}{
		{"loopback proxy", "https://work.sous.example", "", true},
		{"direct TLS", "https://work.sous.example:9443", "listen: 0.0.0.0:9443\ntls_cert: cert.pem\ntls_key: key.pem\n", true},
		{"public cleartext backend", "https://work.sous.example", "listen: 0.0.0.0:8787\n", false},
		{"http", "http://work.sous.example", "", false},
		{"IP RP", "https://127.0.0.1", "", false},
		{"path", "https://work.sous.example/start", "", false},
		{"query", "https://work.sous.example?x=1", "", false},
		{"not canonical", "https://work.Sous.example:443", "", false},
		{"legacy settings", "https://work.sous.example", "dev: true\n", false},
		{"second document", "https://work.sous.example", "---\npublic_url: https://other.sous.example\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "hub.yaml")
			if err := os.WriteFile(p, []byte("public_url: "+tc.origin+"\ndata_dir: private\nweb_dir: web\n"+tc.overrides), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := LoadHosted(p)
			if tc.valid != (err == nil) {
				t.Fatalf("unexpected config result %v", err)
			}
			if !tc.valid {
				return
			}
			if !cfg.HostedWorkspace || cfg.LocalWorkspace || !cfg.Web.Disabled || !cfg.MCP.Disabled || !strings.HasSuffix(cfg.DataDir, string(filepath.Separator)+"private") {
				t.Fatalf("incorrect assembly %+v", cfg)
			}
		})
	}
}

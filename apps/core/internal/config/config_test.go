package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hub.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsAndRoleFallback(t *testing.T) {
	p := write(t, `
providers:
  - name: deepseek
    kind: openai-chat
    base_url: https://api.deepseek.com
    api_key_env: TEST_SOUS_KEY
roles:
  supervisor: deepseek/deepseek-chat
`)
	t.Setenv("TEST_SOUS_KEY", "sk-test")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8787" || c.DataDir != filepath.Join(filepath.Dir(p), "data") {
		t.Fatalf("defaults: %+v", c)
	}
	if prov, model := c.Model(RoleInspector); prov != "deepseek" || model != "deepseek-chat" {
		t.Fatalf("inspector fallback: %s/%s", prov, model)
	}
	if c.Providers[0].APIKey() != "sk-test" {
		t.Fatal("api key not read from env")
	}
	if c.RPID() != "localhost" || c.Secure() {
		t.Fatalf("rpid %q secure %v", c.RPID(), c.Secure())
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"unknown provider": "providers: [{name: a, kind: mock}]\nroles: {supervisor: b/m}\n",
		"bad role ref":     "providers: [{name: a, kind: mock}]\nroles: {supervisor: a}\n",
		"http public url":  "public_url: http://agent.example.com\nproviders: [{name: a, kind: mock}]\nroles: {supervisor: a/m}\n",
		"duplicate":        "providers: [{name: a, kind: mock}, {name: a, kind: mock}]\nroles: {supervisor: a/m}\n",
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// Models are configured on the page: a file without roles (or providers) is fine.
func TestRolesAreOptional(t *testing.T) {
	for _, body := range []string{"", "providers: [{name: a, kind: mock}]\n"} {
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("%q: %v", body, err)
		}
	}
}

func TestHTTPSPublicURL(t *testing.T) {
	c, err := Load(write(t, "public_url: https://agent.wxian.cc/\nproviders: [{name: a, kind: mock}]\nroles: {supervisor: a/m}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.RPID() != "agent.wxian.cc" || !c.Secure() || c.PublicURL != "https://agent.wxian.cc" {
		t.Fatalf("%+v", c)
	}
}

func TestEnvFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("# local secrets\nTEST_SOUS_A=from-file\nexport TEST_SOUS_B=\"quoted value\"\nTEST_SOUS_C=from-file\n"), 0o600)
	p := filepath.Join(dir, "hub.yaml")
	os.WriteFile(p, []byte("env_file: .env\nproviders: [{name: a, kind: mock}]\nroles: {supervisor: a/m}\n"), 0o600)
	t.Setenv("TEST_SOUS_C", "from-env")
	t.Setenv("TEST_SOUS_A", "")
	os.Unsetenv("TEST_SOUS_A")
	t.Setenv("TEST_SOUS_B", "")
	os.Unsetenv("TEST_SOUS_B")
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("TEST_SOUS_A") != "from-file" || os.Getenv("TEST_SOUS_B") != "quoted value" || os.Getenv("TEST_SOUS_C") != "from-env" {
		t.Fatalf("A=%q B=%q C=%q", os.Getenv("TEST_SOUS_A"), os.Getenv("TEST_SOUS_B"), os.Getenv("TEST_SOUS_C"))
	}
}

func TestMissingEnvFileIsFine(t *testing.T) {
	if _, err := Load(write(t, "env_file: nope.env\nproviders: [{name: a, kind: mock}]\nroles: {supervisor: a/m}\n")); err != nil {
		t.Fatal(err)
	}
}

func TestPlainListenOutsideLoopbackIsRefused(t *testing.T) {
	base := func() *Hub {
		return &Hub{PublicURL: "https://a.example", Providers: []Provider{{Name: "m", Kind: "mock"}},
			Roles: map[string]string{RoleSupervisor: "m/x"}}
	}
	for addr, ok := range map[string]bool{"127.0.0.1:8787": true, "localhost:1": true, "[::1]:8787": true, "0.0.0.0:8787": false, ":8787": false} {
		c := base()
		c.Listen = addr
		if err := c.Validate(); (err == nil) != ok {
			t.Errorf("listen %s: %v", addr, err)
		}
	}
	c := base()
	c.Listen, c.TLSCert, c.TLSKey = ":8443", "c.pem", "k.pem"
	if err := c.Validate(); err != nil {
		t.Errorf("TLS listen: %v", err)
	}
	c = base()
	c.Listen, c.InsecureListen = ":8787", true
	if err := c.Validate(); err != nil {
		t.Errorf("insecure_listen: %v", err)
	}
	c = base()
	c.Listen, c.Efforts = "127.0.0.1:1", map[string]string{RoleSupervisor: "extreme"}
	if err := c.Validate(); err == nil {
		t.Error("bad effort accepted")
	}
}

func TestWebSearchConfig(t *testing.T) {
	c, err := Load(write(t, "web:\n  search:\n    provider: brave\n    api_key_env: TEST_SOUS_SEARCH_KEY\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SOUS_SEARCH_KEY", "bk")
	if c.Web.Search.Provider != "brave" || c.Web.Search.APIKey() != "bk" || c.Web.Disabled {
		t.Fatalf("%+v", c.Web)
	}
	for _, ok := range []string{"", "web: {disabled: true}\n", "web: {search: {provider: searxng, base_url: 'http://localhost:8080'}}\n", "web: {search: {provider: duckduckgo}}\n"} {
		if _, err := Load(write(t, ok)); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for name, bad := range map[string]string{
		"unknown engine":    "web: {search: {provider: bing}}\n",
		"brave without key": "web: {search: {provider: brave}}\n",
		"searxng no url":    "web: {search: {provider: searxng}}\n",
		"searxng bad url":   "web: {search: {provider: searxng, base_url: searx.example.org}}\n",
	} {
		if _, err := Load(write(t, bad)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

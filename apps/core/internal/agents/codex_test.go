package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexConfigAuth(t *testing.T) {
	env := map[string]string{"MY_KEY": "k"}
	getenv := func(k string) string { return env[k] }
	cases := []struct {
		name, config string
		want         *bool
	}{
		{"no config", "", nil},
		{"default provider", `model = "gpt-5"`, nil},
		{"openai provider", `model_provider = "openai"`, nil},
		{"bearer token", "model_provider = \"custom\"\n[model_providers.custom]\nbase_url = \"https://x\"\nrequires_openai_auth = false\nexperimental_bearer_token = \"t\"\n", yes()},
		{"env key set", "model_provider = \"p\"\n[model_providers.p]\nenv_key = \"MY_KEY\"\n", yes()},
		{"env key missing", "model_provider = \"p\"\n[model_providers.p]\nenv_key = \"OTHER_KEY\"\n", nil},
		{"requires openai auth", "model_provider = \"p\"\n[model_providers.p]\nrequires_openai_auth = true\nexperimental_bearer_token = \"t\"\n", nil},
		{"unknown provider", `model_provider = "nope"`, nil},
		{"local provider", `model_provider = "ollama"`, yes()},
		{"open endpoint", "model_provider = \"p\"\n[model_providers.p]\nbase_url = \"http://localhost:1\"\n", yes()},
		{"profile wins", "model_provider = \"openai\"\nprofile = \"w\"\n[profiles.w]\nmodel_provider = \"p\"\n[model_providers.p]\nenv_key = \"MY_KEY\"\n", yes()},
		{"bad toml", "model_provider = ", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := codexConfigAuth(dir, getenv)
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Fatalf("got %v, want %v", deref(got), deref(c.want))
			}
		})
	}
}

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

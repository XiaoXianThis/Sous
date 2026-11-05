package hub

import (
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
)

func names(cfg *config.Hub) map[string]bool {
	out := map[string]bool{}
	for _, t := range webTools(cfg) {
		out[t.Spec().Name] = true
	}
	return out
}

func TestWebTools(t *testing.T) {
	// No configuration: DuckDuckGo needs no key.
	if n := names(&config.Hub{}); !n["web_search"] || !n["web_fetch"] {
		t.Errorf("default: %v", n)
	}
	if n := names(&config.Hub{Web: config.Web{Disabled: true}}); len(n) != 0 {
		t.Errorf("disabled: %v", n)
	}
	// A keyed engine whose key is missing leaves search out instead of offering a broken tool.
	cfg := &config.Hub{Web: config.Web{Search: config.WebSearch{Provider: "brave", APIKeyEnv: "SOUS_TEST_BRAVE_KEY"}}}
	t.Setenv("SOUS_TEST_BRAVE_KEY", "")
	if n := names(cfg); n["web_search"] || !n["web_fetch"] {
		t.Errorf("missing key: %v", n)
	}
	t.Setenv("SOUS_TEST_BRAVE_KEY", "k")
	if n := names(cfg); !n["web_search"] {
		t.Errorf("with key: %v", n)
	}
}

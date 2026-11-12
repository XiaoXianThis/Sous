package node

import (
	"crypto/sha256"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

func TestRequestMatchBindsContent(t *testing.T) {
	h := func(s string) string { x := sha256.Sum256([]byte(s)); return trust.B64.EncodeToString(x[:]) }

	m := RequestMatch(proto.OpConfigWrite, map[string]any{"agent": "claude", "file": "settings", "content": "{}", "base_sha256": "x", "cols": 80.0})
	if m["content_sha256"] != h("{}") || m["agent"] != "claude" || m["file"] != "settings" || m["content"] != "" || m["base_sha256"] != "" {
		t.Fatalf("config.write: %v", m)
	}
	m = RequestMatch(proto.OpSetWorkspaces, map[string]any{"workspaces": []any{"~/code", "/srv"}})
	if m["workspaces_sha256"] != h("~/code\x00/srv") {
		t.Fatalf("workspaces: %v", m)
	}
	if m = RequestMatch(proto.OpInstall, map[string]any{"agent": "codex"}); m["upgrade"] != "false" {
		t.Fatalf("install without upgrade: %v", m)
	}
	if m = RequestMatch(proto.OpInstall, map[string]any{"agent": "codex", "upgrade": true}); m["upgrade"] != "true" {
		t.Fatalf("upgrade: %v", m)
	}
	if m = RequestMatch(proto.OpSetEnv, map[string]any{"agent": "codex", "name": "K"}); m["value_sha256"] != h("") {
		t.Fatalf("empty value: %v", m)
	}
}

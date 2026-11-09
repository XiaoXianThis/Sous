package node

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
)

func sensitivePath(p string) bool {
	l := strings.ToLower(filepath.ToSlash(p))
	if oneString(filepath.Base(l), ".zshrc", ".bashrc", ".zprofile", ".profile", "node.json", "outbox.jsonl") {
		return true
	}
	for _, part := range strings.Split(l, "/") {
		if part == ".env" || strings.HasPrefix(part, ".env.") && !strings.Contains(part, "example") && !strings.Contains(part, "sample") || part == ".ssh" || part == ".aws" || part == ".gnupg" || part == ".kube" || part == "keychains" || part == "id_rsa" || part == "id_ed25519" || part == ".netrc" || part == ".git-credentials" || part == ".npmrc" || part == ".pypirc" {
			return true
		}
	}
	if strings.Contains(l, "/.sous/") && !strings.Contains(l, "/.sous/worktrees/") && !strings.Contains(l, "/.sous/scratch/") {
		return true
	}
	return strings.HasPrefix(l, "/etc/shadow") || strings.HasPrefix(l, "/etc/gshadow")
}
func exactConfig(path string) string {
	p := filepath.Clean(ExpandPath(path))
	home := ExpandPath("~")
	switch p {
	case filepath.Join(home, ".zshrc"):
		return "shell"
	case filepath.Join(home, ".ssh", "config"):
		return "ssh"
	}
	return ""
}
func configProjection(kind string, b []byte) string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) == 0 {
			continue
		}
		if redact.SecretName(trim) || strings.Contains(trim, "PRIVATE KEY") {
			continue
		}
		switch kind {
		case "shell":
			if strings.HasPrefix(trim, "export PATH=") || strings.HasPrefix(trim, "PATH=") {
				// Never forward command substitutions, pipelines or executable values.
				if !strings.ContainsAny(trim, "`;&|\n") && !strings.Contains(trim, "$(") {
					out = append(out, redact.Mask(line))
				}
			}
		case "ssh":
			if oneString(strings.ToLower(fields[0]), "host", "hostname", "user", "port") {
				out = append(out, redact.Mask(line))
			}
		}
	}
	return strings.Join(out, "\n")
}
func oneString(v string, vs ...string) bool {
	for _, s := range vs {
		if v == s {
			return true
		}
	}
	return false
}
func maskJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return mask(string(raw))
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, v := range t {
				if redact.SecretName(k) {
					t[k] = "***"
				} else {
					t[k] = walk(v)
				}
			}
		case []any:
			for i, v := range t {
				t[i] = walk(v)
			}
		}
		return x
	}
	b, err := json.MarshalIndent(walk(v), "", "  ")
	if err != nil {
		return fmt.Sprint(err)
	}
	return mask(string(b))
}

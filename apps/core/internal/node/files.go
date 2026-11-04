package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// The config files the page can edit: each agent's own files (listed in
// internal/agents) and this machine's node.json, as agent "sous", file
// "node". The device token never leaves the machine.

const (
	SousAgent    = "sous"
	NodeFile     = "node"
	maxFileBytes = 1 << 20
	maskedToken  = "***"
)

type fileTarget struct {
	path   string
	format string
	node   bool
}

func (a *admin) target(agent, file string) (fileTarget, error) {
	if agent == SousAgent {
		if file != NodeFile {
			return fileTarget{}, nodeErr(proto.ErrCodeNotFound, "没有这个文件")
		}
		return fileTarget{path: configPath(a.d.root), format: "json", node: true}, nil
	}
	spec, err := a.spec(agent)
	if err != nil {
		return fileTarget{}, err
	}
	c, ok := spec.ConfigFile(file)
	if !ok {
		return fileTarget{}, nodeErr(proto.ErrCodeNotFound, "%s 没有可以编辑的文件 %q", spec.Title, file)
	}
	return fileTarget{path: c.Path, format: c.Format}, nil
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// readRaw returns the file and its digest; a missing file has digest "".
func readRaw(path string) ([]byte, string, bool, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if fi.IsDir() {
		return nil, "", false, nodeErr(proto.ErrCodeInvalid, "%s 是一个目录", path)
	}
	if fi.Size() > maxFileBytes {
		return nil, "", false, nodeErr(proto.ErrCodeInvalid, "%s 超过 1 MB，不能在页面上编辑", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", false, err
	}
	return raw, digest(raw), true, nil
}

func (a *admin) readConfig(agent, file string) (proto.ConfigFile, error) {
	t, err := a.target(agent, file)
	if err != nil {
		return proto.ConfigFile{}, err
	}
	raw, sum, exists, err := readRaw(t.path)
	if err != nil {
		return proto.ConfigFile{}, err
	}
	content := string(raw)
	if t.node {
		if content, err = maskNodeConfig(raw); err != nil {
			return proto.ConfigFile{}, err
		}
	}
	return proto.ConfigFile{Agent: agent, File: file, Path: t.path, Format: t.format, Exists: exists, Content: content, SHA256: sum}, nil
}

func (a *admin) writeConfig(args proto.ConfigArgs) (proto.ConfigFile, error) {
	t, err := a.target(args.Agent, args.File)
	if err != nil {
		return proto.ConfigFile{}, err
	}
	if len(args.Content) > maxFileBytes {
		return proto.ConfigFile{}, nodeErr(proto.ErrCodeInvalid, "内容超过 1 MB")
	}
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	raw, sum, exists, err := readRaw(t.path)
	if err != nil {
		return proto.ConfigFile{}, err
	}
	if sum != args.BaseSHA256 {
		return proto.ConfigFile{}, nodeErr(proto.ErrCodeConflict, "文件在你打开之后被改过了，请重新加载再改")
	}
	content := []byte(args.Content)
	if t.node {
		cfg, err := a.mergeNodeConfig(content)
		if err != nil {
			return proto.ConfigFile{}, err
		}
		if content, err = json.MarshalIndent(cfg, "", "  "); err != nil {
			return proto.ConfigFile{}, err
		}
	} else if err := validate(t.format, content); err != nil {
		return proto.ConfigFile{}, nodeErr(proto.ErrCodeInvalid, "格式不对，没有保存：%v", err)
	}

	mode := os.FileMode(0o600)
	backup := ""
	if exists {
		if fi, err := os.Stat(t.path); err == nil {
			mode = fi.Mode().Perm()
		}
		backup = t.path + ".sous-bak"
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return proto.ConfigFile{}, fmt.Errorf("备份失败，没有保存：%w", err)
		}
	}
	if err := writeFileAtomic(t.path, content, mode); err != nil {
		return proto.ConfigFile{}, err
	}
	if t.node {
		cfg, err := LoadConfig(a.d.root)
		if err != nil {
			return proto.ConfigFile{}, err
		}
		a.d.apply(cfg)
		a.kick()
	} else {
		a.kick() // the agent's login or version may follow its config
	}
	out, err := a.readConfig(args.Agent, args.File)
	out.Backup = backup
	return out, err
}

// maskNodeConfig hides the device token and the API keys in agent_env.
func maskNodeConfig(raw []byte) (string, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("node.json: %w", err)
	}
	c.Token = maskedToken
	masked := map[string]map[string]string{}
	for agent, env := range c.AgentEnv {
		masked[agent] = map[string]string{}
		for k := range env {
			masked[agent][k] = maskedToken
		}
	}
	if len(masked) > 0 {
		c.AgentEnv = masked
	}
	out, err := json.MarshalIndent(c, "", "  ")
	return string(out), err
}

// mergeNodeConfig reads an edited node.json. What identifies the machine
// to the Hub, and whether it accepts remote administration, stay as they
// are on disk whatever the edit says.
func (a *admin) mergeNodeConfig(content []byte) (Config, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	var next Config
	if err := dec.Decode(&next); err != nil {
		return Config{}, nodeErr(proto.ErrCodeInvalid, "node.json 格式不对，没有保存：%v", err)
	}
	cur, err := LoadConfig(a.d.root)
	if err != nil {
		return Config{}, err
	}
	next.HubURL, next.NodeID, next.Token, next.RemoteAdmin = cur.HubURL, cur.NodeID, cur.Token, cur.RemoteAdmin
	next.RemoteShell = cur.RemoteShell
	// From the Hub, safety settings only tighten: a server stays a server
	// (as setRole), the floor rules cannot be turned off or replaced, and
	// extra rules can be added but not removed. Loosening them takes an
	// edit on this machine.
	if cur.RoleName() == proto.RoleServer {
		next.Role = cur.Role
	}
	next.Policy.FloorDisabled, next.Policy.Floor = cur.Policy.FloorDisabled, cur.Policy.Floor
	for _, r := range cur.Policy.ExtraFloor {
		if !slices.Contains(next.Policy.ExtraFloor, r) {
			next.Policy.ExtraFloor = append(next.Policy.ExtraFloor, r)
		}
	}
	// A masked key keeps its value; a new value replaces it.
	for agent, env := range next.AgentEnv {
		for k, v := range env {
			if v == maskedToken {
				if old, ok := cur.AgentEnv[agent][k]; ok {
					env[k] = old
				} else {
					delete(env, k)
				}
			}
		}
	}
	if _, err := policy.New(next.Policy); err != nil {
		return Config{}, nodeErr(proto.ErrCodeInvalid, "底线规则不对，没有保存：%v", err)
	}
	if next.Concurrency < 0 {
		return Config{}, nodeErr(proto.ErrCodeInvalid, "concurrency 不能是负数")
	}
	return next, nil
}

func validate(format string, b []byte) error {
	switch format {
	case "json":
		if len(bytes.TrimSpace(b)) == 0 {
			return nil
		}
		var v any
		return json.Unmarshal(b, &v)
	case "jsonc":
		if len(bytes.TrimSpace(b)) == 0 {
			return nil
		}
		var v any
		return json.Unmarshal(StripJSONC(b), &v)
	case "toml":
		var v map[string]any
		return toml.Unmarshal(b, &v)
	case "yaml":
		var v any
		return yaml.Unmarshal(b, &v)
	}
	return nil
}

// StripJSONC removes // and /* */ comments and trailing commas, leaving
// strings alone, so JSON with comments can be checked.
func StripJSONC(b []byte) []byte {
	var out bytes.Buffer
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out.WriteByte(c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out.WriteByte('\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
		case c == ',':
			// Drop it when only whitespace or comments separate it from } or ].
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue
			}
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

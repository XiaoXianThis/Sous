package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Only well-understood local invocations can bind an enduring permission.
// Unknown interpreters, imported dependencies, ambient shell hooks or partial
// evidence deliberately produce no key, even if a model calls them harmless.
func (r *run) bindApprovalIdentity(req policy.Request, cat policy.Category, f *proto.PolicyFacts, ev *proto.ApprovalEvidence) {
	r.mu.Lock()
	launch := append([]string{}, r.approvalLaunch...)
	env := append([]string{}, r.approvalEnv...)
	r.mu.Unlock()
	if len(launch) == 0 {
		return
	}
	if r.spec.TaskID == "" || f.Version != 1 || f.ParseStatus != "ok" || !ev.Complete || len(f.Hard) > 0 || len(f.HardRead) > 0 || cat == policy.Floor || cat == policy.Irreversible || policy.Restricted(f.EffectiveMode) {
		return
	}
	if f.Role == policy.RoleServer {
		for _, effect := range f.Effects {
			if oneString(effect, "edit-worktree", "delete", "service", "irreversible") {
				return
			}
		}
		if f.OpaqueExecution {
			return
		}
	}
	var raw map[string]any
	if json.Unmarshal(req.RawInput, &raw) != nil {
		return
	}
	delete(raw, "description")
	for k := range raw {
		if !oneString(k, "command", "cmd", "commandLine", "script", "path", "file_path", "filePath", "paths", "cwd", "workdir", "timeout", "timeout_ms", "env", "shell", "content", "old_string", "new_string", "replace_all") {
			return
		}
	}
	paths := map[string]string{}
	bindPaths := func(x policy.CommandAnalysis) bool {
		for _, v := range x.Commands {
			for _, redir := range v.Redirections {
				p := strings.TrimLeft(redir, "<>")
				for _, cwd := range v.CWDs {
					if cwd == "" {
						return false
					}
					abs := p
					if !filepath.IsAbs(abs) {
						abs = filepath.Join(cwd, p)
					}
					real, err := filepath.EvalSymlinks(abs)
					if err != nil || sensitivePath(real) {
						return false
					}
					paths[abs] = real
				}
			}
		}
		return true
	}
	cmd := policy.CommandOf(req)
	if cmd != "" {
		x := policy.AnalyzeCommand(cmd, policy.CommandContext{Workdir: req.Workdir, CWD: req.CWD, Shell: req.Shell})
		if x.Status != "ok" {
			return
		}
		if !identityCommands(x) || !bindPaths(x) {
			return
		}
		for _, s := range ev.Scripts {
			if !s.Complete || s.SHA256 == "" {
				return
			}
			// Never decide from masked script text. Read and analyse the original again.
			if s.Path == "(inline program)" {
				if !shellScript(s.Language, "") {
					return
				}
				continue
			}
			b, _, err := safeSnapshot(s.Path, req.Workdir, true)
			if err != nil || digest(b) != s.SHA256 || !shellScript(s.Language, s.Path) {
				return
			}
			sx := policy.AnalyzeCommand(string(b), policy.CommandContext{Workdir: req.Workdir, Shell: "sh"})
			if !identityCommands(sx) || len(sx.Scripts) > 0 || !bindPaths(sx) {
				return
			}
		}
	} else if !oneString(req.ToolKind, "read", "search", "edit", "move", "delete") {
		return
	}
	root, err := filepath.EvalSymlinks(req.Workdir)
	if err != nil {
		return
	}
	fi, err := os.Stat(root)
	if err != nil {
		return
	}
	sys := reflect.ValueOf(fi.Sys())
	if sys.Kind() == reflect.Pointer {
		sys = sys.Elem()
	}
	physical := []any{}
	if sys.IsValid() && sys.Kind() == reflect.Struct {
		for _, name := range []string{"Dev", "Ino", "VolumeSerialNumber", "FileIndexHigh", "FileIndexLow"} {
			v := sys.FieldByName(name)
			if v.IsValid() && v.CanInterface() {
				physical = append(physical, name, v.Interface())
			}
		}
	}
	if len(physical) == 0 {
		return
	}
	branch := strings.TrimSpace(gitEvidence(context.Background(), root, "symbolic-ref", "--quiet", "--short", "HEAD"))
	if branch == "" {
		return
	}
	cwd := req.CWD
	if cwd == "" {
		cwd = root
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return
	}
	for _, v := range env {
		if strings.HasPrefix(v, "BASH_ENV=") || strings.HasPrefix(v, "ENV=") || strings.HasPrefix(v, "CDPATH=") {
			return
		}
	}
	for _, p := range policy.RequestPaths(req) {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, p)
		}
		real, e := filepath.EvalSymlinks(abs)
		if e != nil {
			return
		}
		paths[p] = real
	}
	// The original, unmasked execution values are hashed locally and never sent.
	identity := struct {
		Task, Node, Agent, Role, Mode, Root, CWD, Branch, Kind, Tool, Category, Version string
		Physical                                                                        []any
		Raw                                                                             map[string]any
		Paths                                                                           map[string]string
		Environment                                                                     []string
		Launch                                                                          []string
		Facts                                                                           proto.PolicyFacts
		Scripts                                                                         []proto.ScriptEvidence
		Policy                                                                          policy.Config
	}{r.spec.TaskID, r.d.conf().NodeID, r.spec.Agent, f.Role, f.EffectiveMode, root, cwd, branch, req.ToolKind, req.Title, string(cat), policy.PolicyVersion, physical, raw, paths, env, launch, *f, nil, r.d.conf().Policy}
	// Titles are display-only for shell/file tools; tool kind and schema remain.
	identity.Tool = ""
	for _, s := range ev.Scripts {
		s.Content = ""
		s.ReadAt = 0
		s.Status = ""
		s.DiffStat = ""
		s.Origin = ""
		identity.Scripts = append(identity.Scripts, s)
	}
	b, err := json.Marshal(identity)
	if err != nil {
		return
	}
	f.OperationKey = digest(b)
	f.Reusable = true
}
func identityCommands(x policy.CommandAnalysis) bool {
	if x.Status != "ok" {
		return false
	}
	for _, v := range x.Commands {
		if len(v.Argv) == 0 {
			continue
		}
		n := filepath.Base(v.Argv[0])
		if !oneString(n, "sh", "bash", "dash", "echo", "printf", "pwd", "true", "false", "cd", "export") {
			return false
		}
		for _, arg := range v.Argv {
			if strings.ContainsAny(arg, "$`*?") {
				return false
			}
		}
		for _, s := range v.Assignments {
			if strings.ContainsAny(s, "$`(") {
				return false
			}
		}
	}
	return true
}
func revalidateEvidence(ev *proto.ApprovalEvidence, root string) bool {
	if ev == nil {
		return true
	}
	for _, s := range ev.Scripts {
		if s.Path == "(inline program)" || s.SHA256 == "" {
			continue
		}
		b, real, err := safeSnapshot(s.Path, root, true)
		if err != nil || real != s.Path || digest(b) != s.SHA256 {
			return false
		}
	}
	return true
}

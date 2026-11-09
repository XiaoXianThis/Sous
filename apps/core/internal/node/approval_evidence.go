package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// safeSnapshot opens once, checks the same handle and the path afterwards,
// and refuses devices, links into secrets, binaries and oversized files.
func safeSnapshot(path, root string, temporary bool) ([]byte, string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, path)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, "", err
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", err
	}
	allowed := within(rr, real)
	if temporary {
		tmp, e := filepath.EvalSymlinks(scriptTempDir())
		allowed = allowed || (e == nil && within(tmp, real))
	}
	if !allowed || inGitDir(filepath.ToSlash(abs)) || inGitDir(filepath.ToSlash(real)) || sensitivePath(abs) || sensitivePath(real) {
		return nil, "", fmt.Errorf("path is outside evidence scope or sensitive")
	}
	initial, e := os.Lstat(real)
	if e != nil || !initial.Mode().IsRegular() {
		return nil, "", fmt.Errorf("not a regular file")
	}
	f, err := openSnapshot(real)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 || !os.SameFile(initial, before) {
		return nil, "", fmt.Errorf("not a regular text file within 1 MiB")
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, "", err
	}
	after, e := f.Stat()
	cur, e2 := os.Stat(abs)
	if e != nil || e2 != nil || !os.SameFile(before, after) || !os.SameFile(after, cur) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(b) > 1<<20 {
		return nil, "", fmt.Errorf("file changed during evidence collection")
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return nil, "", fmt.Errorf("binary file")
	}
	return b, real, nil
}
func gitEvidence(ctx context.Context, root string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	argv := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=", "-c", "diff.external=", "-C", root}
	argv = append(argv, args...)
	c := exec.CommandContext(ctx, "git", argv...)
	c.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1")
	out := &capped{max: 16384}
	c.Stdout = out
	c.Stderr = io.Discard
	if c.Run() != nil || out.cut {
		return ""
	}
	return mask(out.b.String())
}
func (r *run) evidenceBaseline(root string) {
	// A bounded start snapshot; paths outside the snapshot keep unknown origin.
	names := gitEvidence(r.ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	base := map[string]string{}
	budget := 4 << 20
	for _, rel := range strings.Split(names, "\x00") {
		if len(base) >= 128 || budget <= 0 {
			break
		}
		ext := filepath.Ext(rel)
		if !strings.Contains(" .sh .bash .py .js .ts .rb .pl ", " "+ext+" ") || ext == "" {
			continue
		}
		b, real, err := safeSnapshot(rel, root, false)
		if err == nil {
			budget -= len(b)
			base[real] = digest(b)
		}
	}
	r.mu.Lock()
	r.baseline = base
	r.mu.Unlock()
}
func collectEvidence(cmd, root string, baseline map[string]string) (proto.ApprovalEvidence, []policy.Request) {
	return collectEvidenceContext(cmd, root, baseline, policy.CommandContext{Workdir: root})
}
func collectEvidenceContext(cmd, root string, baseline map[string]string, shellContext policy.CommandContext) (proto.ApprovalEvidence, []policy.Request) {
	x := policy.AnalyzeCommand(cmd, shellContext)
	ev := proto.ApprovalEvidence{Version: 1, Complete: x.Status == "ok"}
	if x.Status != "ok" {
		ev.Failure = x.Failure
		return ev, nil
	}
	requests := []policy.Request{}
	guardProof := os.Getenv("BASH_ENV") == "" && os.Getenv("ENV") == "" && !strings.Contains(cmd, "BASH_ENV") && !strings.Contains(cmd, "ENV=")
	remaining := 32 << 10
	for i, s := range x.Scripts {
		if !guardProof {
			s.UnsetEnv = nil
		}
		if i >= 4 {
			ev.Complete = false
			ev.Failure = "script count budget"
			break
		}
		item := proto.ScriptEvidence{Path: s.Path, Language: s.Language, ReadAt: time.Now().UnixMilli(), Origin: "unknown"}
		var b []byte
		if s.Inline != "" {
			b = []byte(s.Inline)
			item.Path = "(inline program)"
			item.Inside = true
		} else if s.CWD != "" {
			abs := s.Path
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(s.CWD, s.Path)
			}
			physicalRoot, _ := filepath.EvalSymlinks(root)
			physicalCWD, _ := filepath.EvalSymlinks(s.CWD)
			if physicalCWD == "" || !within(physicalRoot, physicalCWD) {
				item.Failure = "cwd outside worktree"
			} else {
				var err error
				b, item.Path, err = safeSnapshot(abs, root, true)
				if err != nil {
					item.Path = abs
					item.Failure = err.Error()
				} else {
					item.Inside = within(physicalRoot, item.Path)
				}
			}
		} else {
			item.Failure = "unknown cwd"
		}
		if item.Failure == "" {
			if len(s.UnsetEnv) > 0 && policy.ReachableShell(string(b), s.UnsetEnv) != string(b) {
				item.Reachability = "missing environment guard exits before side effects"
			}
			item.Size = int64(len(b))
			item.SHA256 = digest(b)
			max := min(16<<10, remaining)
			n := min(len(b), max)
			// Filter the full snapshot before truncating, so a clipped JSON field
			// or private-key header cannot hide its sensitive context.
			filtered := mask(string(b))
			item.Content = filtered[:min(n, len(filtered))]
			item.Truncated = n < len(b)
			item.Complete = !item.Truncated
			remaining -= n
			if s.Inline == "" {
				item.Status = gitEvidence(context.Background(), root, "status", "--porcelain=v1", "--", item.Path)
				item.DiffStat = gitEvidence(context.Background(), root, "diff", "--no-ext-diff", "--no-textconv", "--stat", "--", item.Path)
				if old, ok := baseline[item.Path]; ok {
					item.Origin = "present_at_run_start"
					if old != item.SHA256 {
						item.Origin = "changed_since_run_start"
					}
				}
			}
			// Full shell scripts contribute real effects and signals, never execute them.
			if s.Inline == "" && shellScript(s.Language, item.Path) {
				raw, _ := json.Marshal(map[string]string{"command": string(b)})
				requests = append(requests, policy.Request{ToolKind: "execute", RawInput: raw, Workdir: root, CWD: s.CWD, Shell: "sh", UnsetEnv: s.UnsetEnv})
			}
		} else {
			ev.Complete = false
		}
		if !item.Complete {
			ev.Complete = false
		}
		item.Path = mask(item.Path)
		item.Failure = mask(item.Failure)
		ev.Scripts = append(ev.Scripts, item)
	}
	// Only syntactically explicit paths, never words extracted from document data.
	seen := map[string]bool{}
	for _, v := range x.Commands {
		if len(v.Argv) == 0 {
			continue
		}
		for _, p := range v.Argv[1:] {
			if len(ev.Paths) >= 16 {
				break
			}
			if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~/") {
				continue
			}
			if seen[p] || within(root, p) {
				continue
			}
			seen[p] = true
			pe := proto.PathEvidence{Path: mask(p)}
			fi, err := os.Lstat(ExpandPath(p))
			if err == nil {
				pe.Exists = true
				pe.Size = fi.Size()
				pe.Kind = "file"
				if fi.IsDir() {
					pe.Kind = "directory"
				}
				if fi.Mode()&os.ModeSymlink != 0 {
					pe.Kind = "symlink"
				}
			}
			ev.Paths = append(ev.Paths, pe)
		}
	}
	return ev, requests
}
func shellScript(lang, path string) bool {
	return lang == "sh" || lang == "bash" || lang == "dash" || strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".bash")
}
func (r *run) approvalEvidence(req policy.Request, cat policy.Category) (*proto.PolicyFacts, *proto.ApprovalEvidence, policy.Category, policy.Assessment) {
	req = policy.ResolveRequest(req)
	r.mu.Lock()
	base := r.baseline
	executionEnv := append([]string{}, r.approvalEnv...)
	r.mu.Unlock()
	cmd := policy.CommandOf(req)
	ev, scripts := collectEvidenceContext(cmd, req.Workdir, base, policy.CommandContext{Workdir: req.Workdir, CWD: req.CWD, Shell: req.Shell})
	a := r.d.pol().Assess(req, cat)
	x := policy.AnalyzeCommand(cmd, policy.CommandContext{Workdir: req.Workdir, CWD: req.CWD, Shell: req.Shell})
	guardProof := !strings.Contains(string(req.RawInput), "BASH_ENV") && !strings.Contains(string(req.RawInput), `"ENV"`)
	for _, v := range executionEnv {
		if strings.HasPrefix(v, "BASH_ENV=") || strings.HasPrefix(v, "ENV=") {
			guardProof = false
		}
	}
	for _, script := range scripts {
		if !guardProof {
			script.UnsetEnv = nil
			for i := range ev.Scripts {
				ev.Scripts[i].Reachability = ""
			}
		}
		c, _ := r.d.pol().Classify(script)
		if categoryRank(c) > categoryRank(cat) {
			cat = c
		}
		more := r.d.pol().Assess(script, c)
		a.Hard = append(a.Hard, more.Hard...)
		a.HardRead = append(a.HardRead, more.HardRead...)
		a.Soft = append(a.Soft, more.Soft...)
		a.Effects = append(a.Effects, more.Effects...)
	}
	facts := &proto.PolicyFacts{Version: 1, PolicyVersion: policy.PolicyVersion, ParseStatus: x.Status, Role: r.d.role(), EffectiveMode: policy.EffectiveMode(r.d.role(), r.spec.PermissionMode), CommandDigest: digest([]byte(cmd)), CWD: mask(firstNonEmpty(req.CWD, req.Workdir)), ReadOnlyProven: x.ReadOnly, WritesOutside: x.WritesOutside, OpaqueExecution: x.Opaque, Hard: a.Hard, HardRead: a.HardRead, Soft: a.Soft, InstallSource: "unknown", EvidenceComplete: ev.Complete}
	for _, c := range a.Effects {
		facts.Effects = append(facts.Effects, string(c))
	}
	if cmd == "" {
		facts.ReadOnlyProven = false
	}
	if oneString(req.ToolKind, "edit", "move", "delete") {
		facts.ReadOnlyProven = false
	}
	if !r.d.pol().ShellEnabled() {
		facts.ParseStatus = "legacy"
		facts.EvidenceComplete = false
	}
	r.bindApprovalIdentity(req, cat, facts, &ev)
	return facts, &ev, cat, a
}
func categoryRank(c policy.Category) int {
	for i, v := range []policy.Category{policy.Read, policy.EditWorktree, policy.ExecSafe, policy.ExecOther, policy.Install, policy.OutsideWorktree, policy.Delete, policy.GitRemote, policy.Service, policy.Irreversible, policy.Floor} {
		if v == c {
			return i
		}
	}
	return 100
}

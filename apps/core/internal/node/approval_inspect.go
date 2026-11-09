package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

type approvalScope struct {
	root    string
	scripts []proto.ScriptEvidence
}

func (a *admin) approvalScope(runID, approvalID string) (*run, approvalScope, error) {
	if a.d.conf().ApprovalInspectDisabled {
		return nil, approvalScope{}, fmt.Errorf("approval inspection disabled locally")
	}
	r := a.d.run(runID)
	if r == nil {
		return nil, approvalScope{}, fmt.Errorf("approval run is not active")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	scope, ok := r.approvalScopes[approvalID]
	if !ok || r.approvals[approvalID] == nil || r.paused || r.ctx.Err() != nil {
		return nil, approvalScope{}, fmt.Errorf("approval is not pending")
	}
	return r, scope, nil
}
func (a *admin) approvalRead(ctx context.Context, args proto.ApprovalReadArgs) (proto.ApprovalReadResult, error) {
	r, scope, err := a.approvalScope(args.RunID, args.ApprovalID)
	if err != nil {
		return proto.ApprovalReadResult{}, err
	}
	if args.Offset < 0 || args.MaxBytes < 0 || args.MaxBytes > 16<<10 {
		return proto.ApprovalReadResult{}, fmt.Errorf("invalid read bounds")
	}
	p := args.Path
	if strings.HasPrefix(p, "~") {
		p = ExpandPath(p)
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(scope.root, p)
	}
	var b []byte
	var hash string
	if filepath.Clean(p) == filepath.Join(a.d.root, "node.json") {
		// Projection from in-memory configuration, never node.json's credential body.
		u, e := url.Parse(a.d.conf().HubURL)
		if e != nil {
			return proto.ApprovalReadResult{}, fmt.Errorf("invalid hub address")
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		_, existsErr := os.Stat(filepath.Join(a.d.root, "hub.db"))
		b, _ = json.Marshal(map[string]any{"hub_url": u.String(), "local_hub_db_exists": existsErr == nil})
		hash = digest(b)
	} else if kind := exactConfig(p); kind != "" {
		// Exact lexical AND physical identity: no symlink aliases or redirection.
		fi, e := os.Lstat(p)
		if e != nil || !fi.Mode().IsRegular() || fi.Size() > 64<<10 {
			return proto.ApprovalReadResult{}, fmt.Errorf("configuration is not a bounded regular file")
		}
		real, e := filepath.EvalSymlinks(p)
		homeReal, _ := filepath.EvalSymlinks(ExpandPath("~"))
		rel, _ := filepath.Rel(ExpandPath("~"), p)
		if e != nil || real != filepath.Join(homeReal, rel) {
			return proto.ApprovalReadResult{}, fmt.Errorf("configuration symlink denied")
		}
		f, e := openSnapshot(real)
		if e != nil {
			return proto.ApprovalReadResult{}, fmt.Errorf("configuration open failed")
		}
		defer f.Close()
		before, e := f.Stat()
		if e != nil || !os.SameFile(fi, before) || !before.Mode().IsRegular() {
			return proto.ApprovalReadResult{}, fmt.Errorf("configuration changed")
		}
		raw, e := io.ReadAll(io.LimitReader(f, (64<<10)+1))
		after, e2 := f.Stat()
		current, e3 := os.Lstat(p)
		if e != nil || e2 != nil || e3 != nil || !os.SameFile(before, current) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(raw) > 64<<10 {
			return proto.ApprovalReadResult{}, fmt.Errorf("configuration read failed")
		}
		b = []byte(configProjection(kind, raw))
		hash = digest(raw)
	} else {
		temporary := false
		for _, s := range scope.scripts {
			if s.Path == p {
				temporary = true
			}
		}
		var real string
		b, real, err = safeSnapshot(p, scope.root, temporary)
		_ = real
		if err != nil {
			return proto.ApprovalReadResult{}, fmt.Errorf("read denied: %s", mask(err.Error()))
		}
		hash = digest(b)
		if len(b) > 64<<10 {
			return proto.ApprovalReadResult{}, fmt.Errorf("file exceeds 64 KiB inspection limit")
		}
		b = []byte(mask(string(b)))
	}
	if r.ctx.Err() != nil {
		return proto.ApprovalReadResult{}, r.ctx.Err()
	}
	maxBytes := args.MaxBytes
	if maxBytes == 0 {
		maxBytes = 16 << 10
	}
	if args.Offset > len(b) {
		return proto.ApprovalReadResult{}, fmt.Errorf("offset beyond file")
	}
	end := min(len(b), args.Offset+maxBytes)
	return proto.ApprovalReadResult{Content: string(b[args.Offset:end]), Size: len(b), SHA256: hash, Complete: args.Offset == 0 && end == len(b)}, nil
}

func (a *admin) inspectProjection(ctx context.Context, args proto.LookArgs) (proto.LookResult, bool, error) {
	_, scope, err := a.approvalScope(args.RunID, args.ApprovalID)
	if err != nil {
		return proto.LookResult{}, true, err
	}
	x := policy.AnalyzeCommand(args.Command, policy.CommandContext{Workdir: scope.root, Shell: "sh"})
	if x.Status != "ok" || len(x.Commands) != 1 || !x.ReadOnly {
		return proto.LookResult{}, false, nil
	}
	v := x.Commands[0]
	if len(v.Redirections) == 0 && len(v.Assignments) == 0 && len(v.Argv) >= 2 && filepath.Base(v.Argv[0]) == "ls" {
		p := filepath.Clean(ExpandPath(v.Argv[len(v.Argv)-1]))
		home := ExpandPath("~")
		if p == filepath.Join(home, "Library", "Keychains") {
			for _, arg := range v.Argv[1 : len(v.Argv)-1] {
				if !oneString(arg, "-a", "-l", "-la", "-al", "-1") {
					return proto.LookResult{}, true, fmt.Errorf("keychain inspection only lists names")
				}
			}
			real, e := filepath.EvalSymlinks(p)
			homeReal, _ := filepath.EvalSymlinks(home)
			if e != nil || real != filepath.Join(homeReal, "Library", "Keychains") {
				return proto.LookResult{}, true, fmt.Errorf("keychain directory symlink denied")
			}
			f, e := os.Open(real)
			if e != nil {
				return proto.LookResult{}, true, e
			}
			defer f.Close()
			entries, e := f.ReadDir(256)
			if e != nil && len(entries) == 0 {
				return proto.LookResult{}, true, e
			}
			var names []string
			for _, entry := range entries {
				names = append(names, mask(entry.Name()))
			}
			out := strings.Join(names, "\n")
			cut := len(entries) == 256 || len(out) > lookBytes
			if len(out) > lookBytes {
				out = out[:lookBytes]
			}
			return proto.LookResult{Output: out, Truncated: cut}, true, nil
		}
	}
	if len(v.Redirections) > 0 || len(v.Argv) < 2 || !oneString(filepath.Base(v.Argv[0]), "cat", "grep", "rg", "head") {
		return proto.LookResult{}, false, nil
	}
	path := v.Argv[len(v.Argv)-1]
	if exactConfig(path) == "" {
		return proto.LookResult{}, false, nil
	}
	// Shell command isn't run; its only possible result is the node projection.
	res, e := a.approvalRead(ctx, proto.ApprovalReadArgs{RunID: args.RunID, ApprovalID: args.ApprovalID, Path: path, MaxBytes: lookBytes})
	return proto.LookResult{Output: res.Content}, true, e
}

func safeLookPaths(cmd, dir string) bool {
	x := policy.AnalyzeCommand(cmd, policy.CommandContext{Workdir: dir, Shell: "sh"})
	if x.Status != "ok" {
		return false
	}
	for _, v := range x.Commands {
		if len(v.Argv) == 0 {
			continue
		}
		if oneString(filepath.Base(v.Argv[0]), "env", "printenv", "set", "export", "declare") {
			return false
		}
		operands := append([]string{}, v.Argv[1:]...)
		for _, redir := range v.Redirections {
			operands = append(operands, strings.TrimLeft(redir, "<>"))
		}
		for _, arg := range operands {
			if strings.HasPrefix(arg, "-") || arg == "" {
				continue
			}
			// Expanding a glob could name a credential absent from the lexical
			// operands. Inspection never evaluates one to discover its scope.
			if strings.ContainsAny(arg, "*?[{$`") {
				return false
			}
			abs := arg
			if strings.HasPrefix(abs, "~") {
				abs = ExpandPath(abs)
			} else if !filepath.IsAbs(abs) {
				abs = filepath.Join(dir, abs)
			}
			if sensitivePath(abs) {
				return false
			}
			if real, err := filepath.EvalSymlinks(abs); err == nil && sensitivePath(real) {
				return false
			}
		}
	}
	return true
}

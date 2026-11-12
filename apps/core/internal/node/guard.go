package node

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/project"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

// The guard (work/specs/zero-trust, design §5): the brain may do on its own
// what the user's autonomy scope allows; anything beyond needs a permit the
// user's session sent here directly; critical operations need a permit
// carrying a fresh assertion (Face ID) over the permit itself. This machine
// checks all of it; it believes neither the brain nor the relay.

// ScopeError starts the error of a run refused for being out of scope; the
// page offers "许可并重试" for it.
const ScopeError = "超出自主范围："

// Admin ops the brain may never send on its own: each needs a critical permit.
var criticalOps = map[string]bool{
	proto.OpInstall: true, proto.OpLogin: true, proto.OpLogout: true, proto.OpSetEnv: true,
	proto.OpConfigWrite: true, proto.OpShellOpen: true, proto.OpSetRole: true, proto.OpSetWorkspaces: true,
}

// Admin ops that only the session that opened the job may send (directly).
var sessionOnlyOps = map[string]bool{proto.OpJobInput: true}

// Violations in this window freeze the machine.
const (
	violationWindow = 10 * time.Minute
	violationLimit  = 3
)

// allow checks a frame from the brain. It answers refusals itself.
func (d *Daemon) allow(ctx context.Context, f proto.Frame) bool {
	d.mu.Lock()
	nt := d.trust
	d.mu.Unlock()
	if nt == nil {
		return true // tests talking to an in-process Hub
	}
	switch f.T {
	case proto.TAck, proto.TWelcome:
		return true
	}
	ok, reason, violation := nt.check(f)
	nt.audit.add("brain", f.T, ok, reason, f.Body)
	if ok {
		return true
	}
	slog.Warn("daemon: refused a frame from the brain", "type", f.T, "reason", reason)
	if violation {
		nt.violation(reason)
	}
	nt.refuse(f, reason)
	return false
}

func (nt *nodeTrust) check(f proto.Frame) (ok bool, reason string, violation bool) {
	if fr := nt.isFrozen(); fr != nil {
		return false, "这台机器已自我冻结：" + fr.Reason, false
	}
	paused := nt.c.Paused()
	switch f.T {
	case proto.TRunStart:
		var rs proto.RunStart
		if f.Decode(&rs) != nil {
			return false, "无法解析", true
		}
		if paused {
			return false, ScopeError + "你暂停了所有工作", false
		}
		if why := nt.outOfScope(rs); why != "" {
			if nt.usePermit(trust.PermitRun, map[string]string{"task_id": rs.TaskID}, false) {
				return true, "许可：" + why, false
			}
			return false, ScopeError + why, false
		}
	case proto.TRunMessage, proto.TRunResume:
		if paused {
			return false, "你暂停了所有工作", false
		}
	case proto.TNodeRequest:
		var req proto.NodeRequest
		if f.Decode(&req) != nil {
			return false, "无法解析", true
		}
		if sessionOnlyOps[req.Op] {
			return false, "终端输入只接受打开它的会话直接发来的", true
		}
		if criticalOps[req.Op] {
			var args map[string]any
			json.Unmarshal(req.Args, &args)
			sid, ok := nt.usePermitMatching(trust.PermitAdmin, RequestMatch(req.Op, args))
			if !ok {
				return false, "“" + req.Op + "”需要你在页面上刷一次 Face ID", true
			}
			// A job it starts (terminal, install, login) takes input only
			// from this session.
			nt.mu.Lock()
			nt.reqOwners[f.ID] = sid
			nt.mu.Unlock()
		}
	case proto.TNodeUpdate:
		var u proto.NodeUpdate
		if f.Decode(&u) != nil {
			return false, "无法解析", true
		}
		if !nt.usePermit(trust.PermitUpdate, map[string]string{"version": u.Version}, true) {
			return false, "安装更新需要你在页面上刷一次 Face ID", true
		}
	}
	return true, "", false
}

// outOfScope explains why a run is outside the user's autonomy scope, or "".
func (nt *nodeTrust) outOfScope(rs proto.RunStart) string {
	key := ""
	switch {
	case rs.RepoPath != "":
		key = projectKeyOf(rs.RepoPath)
	case rs.Setup != nil && rs.Setup.Remote != "":
		key = project.Key(rs.Setup.Remote)
	}
	sc := nt.c.Scopes().For(key)
	id := nt.d.conf().NodeID
	if len(sc.Nodes) > 0 && !slices.Contains(sc.Nodes, id) {
		return "这个项目不允许在这台机器上自主工作"
	}
	switch rs.PermissionMode {
	case "open":
		if !sc.AllowOpen {
			return "open 模式需要你在自主范围里打开"
		}
		if !nt.d.conf().OpenAllowed() {
			return "这台机器的 node.json 关闭了 open 模式"
		}
	case "auto":
		if sc.MaxMode == "default" {
			return "这个项目只允许 default 模式"
		}
	}
	if rs.Kind == "ops" && !sc.AllowOps {
		return "运维和部署需要你许可"
	}
	if rs.Git != nil && rs.Git.Op == "merge" && !sc.AllowMerge {
		return "合并需要你许可"
	}
	return ""
}

// projectKeyOf identifies a repository the way the brain does: by its
// origin remote (the first remote without one), else by its path.
func projectKeyOf(repo string) string {
	repo = ExpandPath(repo)
	out, err := exec.Command("git", "-C", repo, "config", "--get", "remote.origin.url").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		names, _ := exec.Command("git", "-C", repo, "remote").Output()
		if first, _, _ := strings.Cut(strings.TrimSpace(string(names)), "\n"); first != "" {
			out, _ = exec.Command("git", "-C", repo, "config", "--get", "remote."+first+".url").Output()
		}
	}
	if k := project.Key(string(out)); k != "" {
		return k
	}
	abs, _ := filepath.Abs(repo)
	return "local:" + abs
}

// addPermit checks a permit a session sent and keeps it until used or expired.
func (nt *nodeTrust) addPermit(sid string, m sessionMsg) error {
	raw, err := trust.B64.DecodeString(m.Raw)
	if err != nil {
		return err
	}
	var p trust.Permit
	if err := json.Unmarshal(raw, &p); err != nil || p.T != trust.TPermit {
		return errors.New("不是许可")
	}
	now := nt.c.Now()
	if p.SID != sid {
		return errors.New("许可属于另一个会话")
	}
	if p.Node != nt.d.conf().NodeID {
		return errors.New("许可是给另一台机器的")
	}
	if err := trust.CheckFresh(p.IAT, trust.CommandTTL, now); err != nil {
		return fmt.Errorf("许可：%w", err)
	}
	if p.Exp < now.Unix() || p.Exp-p.IAT > int64((15*time.Minute)/time.Second) {
		return errors.New("许可的有效期不对")
	}
	critical := p.Critical || p.Action == trust.PermitAdmin || p.Action == trust.PermitUpdate || p.Action == trust.PermitUnfreeze
	if critical {
		if m.Assert == nil {
			return errors.New("这个许可需要你刷 Face ID")
		}
		r := nt.c.Roster()
		if r == nil {
			return errors.New("还没有名册")
		}
		if err := trust.VerifyAssertion(r, *m.Assert, trust.CriticalChallenge(raw), now, nt.usedChallenge); err != nil {
			return err
		}
		p.Critical = true
	}
	if p.Action == trust.PermitUnfreeze {
		nt.unfreeze()
		return nil
	}
	nt.mu.Lock()
	for id, old := range nt.permits {
		if old.Exp < now.Unix() {
			delete(nt.permits, id)
		}
	}
	if _, dup := nt.permits[p.ID]; dup {
		nt.mu.Unlock()
		return errors.New("许可已经用过")
	}
	nt.permits[p.ID] = p
	nt.mu.Unlock()
	return nil
}

var usedChallenges = struct {
	m map[string]time.Time
}{m: map[string]time.Time{}}

func (nt *nodeTrust) usedChallenge(c string) bool {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	now := time.Now()
	for k, t := range usedChallenges.m {
		if now.Sub(t) > 2*trust.AssertionTTL {
			delete(usedChallenges.m, k)
		}
	}
	_, seen := usedChallenges.m[c]
	usedChallenges.m[c] = now
	return seen
}

// usePermit consumes a permit whose match equals want exactly.
func (nt *nodeTrust) usePermit(action string, want map[string]string, critical bool) bool {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	now := nt.c.Now().Unix()
	for id, p := range nt.permits {
		if p.Action != action || p.Exp < now || (critical && !p.Critical) {
			continue
		}
		ok := true
		for k, v := range want {
			if p.Match[k] != v {
				ok = false
				break
			}
		}
		if ok && (want["task_id"] != "" || action != trust.PermitRun) {
			delete(nt.permits, id)
			return true
		}
	}
	return false
}

// boundArgs are what a critical permit must name for each op, so the brain
// cannot keep the user's approval and change what it approved: the API key
// written, the config file's content, the role, the workspaces. A shell's
// root is not bound: the shell belongs to the session that opened it, and
// that session can cd anywhere anyway.
var boundArgs = map[string][]string{
	proto.OpInstall:       {"agent", "upgrade"},
	proto.OpLogin:         {"agent"},
	proto.OpLogout:        {"agent"},
	proto.OpSetEnv:        {"agent", "name", "value_sha256"},
	proto.OpConfigWrite:   {"agent", "file", "content_sha256"},
	proto.OpShellOpen:     {},
	proto.OpSetRole:       {"role"},
	proto.OpSetWorkspaces: {"workspaces_sha256"},
}

// RequestMatch describes a critical request the way a permit names it:
// strings as they are, booleans as "true"/"false", secrets and file
// contents by SHA-256, and the workspace list by the SHA-256 of its paths
// joined by NUL. The page computes the same (src/lib/api.ts).
func RequestMatch(op string, args map[string]any) map[string]string {
	m := map[string]string{"op": op}
	for k, v := range args {
		switch v := v.(type) {
		case string:
			switch k {
			case "content", "value":
				m[k+"_sha256"] = sha256B64(v)
			case "owner", "base_sha256":
			default:
				m[k] = v
			}
		case bool:
			m[k] = strconv.FormatBool(v)
		case []any:
			if k == "workspaces" {
				parts := make([]string, 0, len(v))
				for _, x := range v {
					s, _ := x.(string)
					parts = append(parts, s)
				}
				m["workspaces_sha256"] = sha256B64(strings.Join(parts, "\x00"))
			}
		}
	}
	for _, k := range boundArgs[op] {
		if _, ok := m[k]; !ok {
			switch {
			case k == "upgrade":
				m[k] = "false" // omitted means a fresh install
			case strings.HasSuffix(k, "_sha256"):
				m[k] = sha256B64("")
			}
		}
	}
	return m
}

func sha256B64(s string) string {
	h := sha256.Sum256([]byte(s))
	return trust.B64.EncodeToString(h[:])
}

// usePermitMatching consumes an admin permit whose every match key equals
// the request's; the permit must name the op and every bound argument.
func (nt *nodeTrust) usePermitMatching(action string, req map[string]string) (string, bool) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	now := nt.c.Now().Unix()
	for id, p := range nt.permits {
		if p.Action != action || p.Exp < now || !p.Critical || p.Match["op"] == "" {
			continue
		}
		bound := true
		for _, k := range boundArgs[p.Match["op"]] {
			if _, ok := p.Match[k]; !ok {
				bound = false
			}
		}
		if !bound {
			continue
		}
		ok := true
		for k, v := range p.Match {
			if req[k] != v {
				ok = false
				break
			}
		}
		if ok {
			delete(nt.permits, id)
			return p.SID, true
		}
	}
	return "", false
}

// jobStarted records who may type into a job a permitted request started.
func (nt *nodeTrust) jobStarted(reqID string, res any) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	sid, ok := nt.reqOwners[reqID]
	delete(nt.reqOwners, reqID)
	if js, isJob := res.(proto.JobStarted); ok && isJob {
		nt.jobOwners[js.JobID] = sid
	}
}

// refuse answers a refused frame so the brain does not wait forever.
func (nt *nodeTrust) refuse(f proto.Frame, reason string) {
	switch f.T {
	case proto.TRunStart:
		var rs proto.RunStart
		f.Decode(&rs)
		nt.d.ob.reliable(proto.New(proto.TRunEnded, proto.RunEnded{RunID: rs.RunID, Status: "failed", Error: reason}))
	case proto.TNodeRequest:
		nt.d.ob.ephemeral(proto.New(proto.TNodeResponse, proto.NodeResponse{ReqID: f.ID, OK: false,
			Error: &proto.NodeError{Code: "permit_required", Message: reason}}))
	case proto.TNodeUpdate:
		nt.d.ob.reliable(proto.New(proto.TNodeUpdateResult, proto.NodeUpdateResult{Error: reason}))
	}
}

func (nt *nodeTrust) violation(reason string) {
	nt.mu.Lock()
	now := time.Now()
	nt.violations = append(slices.DeleteFunc(nt.violations, func(t time.Time) bool { return now.Sub(t) > violationWindow }), now)
	n := len(nt.violations)
	nt.mu.Unlock()
	if n >= violationLimit {
		nt.freeze(fmt.Sprintf("10 分钟内 %d 次收到越权指令（最近一次：%s）", n, reason))
	}
}

func (nt *nodeTrust) isFrozen() *frozenState {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	return nt.frozen
}

func (nt *nodeTrust) freeze(reason string) {
	nt.mu.Lock()
	if nt.frozen != nil {
		nt.mu.Unlock()
		return
	}
	nt.frozen = &frozenState{Reason: reason, At: time.Now().Unix()}
	raw, _ := json.Marshal(nt.frozen)
	nt.mu.Unlock()
	os.WriteFile(filepath.Join(trustDir(nt.d.root), "frozen.json"), raw, 0o600)
	slog.Error("daemon: this machine froze itself", "reason", reason)
	nt.audit.add("self", "freeze", true, reason, nil)
	go nt.alert(reason)
}

func (nt *nodeTrust) unfreeze() {
	nt.mu.Lock()
	nt.frozen, nt.violations = nil, nil
	nt.mu.Unlock()
	os.Remove(filepath.Join(trustDir(nt.d.root), "frozen.json"))
	nt.audit.add("self", "unfreeze", true, "", nil)
	go nt.alert("")
}

// alert publishes this machine's state for the page (signed by the machine).
func (nt *nodeTrust) alert(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	doc, err := trust.Sign(nt.c.SignKey(), trust.TAudit, map[string]any{"t": trust.TAudit, "node": nt.d.conf().NodeID,
		"frozen": reason != "", "reason": reason, "iat": time.Now().Unix()})
	if err != nil {
		return
	}
	if err := nt.c.Put(ctx, "alert/"+nt.d.conf().NodeID, doc); err != nil {
		slog.Warn("daemon: cannot publish the alert", "err", err)
	}
}

func versionString() string { return version.Version }

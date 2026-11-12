package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

func putFixture(t *testing.T, root, path, body string) string {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestApprovalEvidenceSample08And09(t *testing.T) {
	needShell(t)
	root := t.TempDir()
	p := putFixture(t, root, "work/tmp/signing/e2e.sh", "#!/bin/sh\nprintf 'temporary fixture only'\n")
	putFixture(t, root, "apps/core/scripts/export-signing-cert.sh", "#!/bin/sh\nif [ -z \"${SOUS_SIGN_CERT_P12_PASSWORD:-}\" ]; then exit 1; fi\nsecurity export -k login\n")
	for _, cmd := range []string{`cd . && timeout 180 bash work/tmp/signing/e2e.sh 2>&1`, `T=$PWD/work/tmp/signing; env -u SOUS_SIGN_CERT_P12_PASSWORD sh apps/core/scripts/export-signing-cert.sh`} {
		ev, requests := collectEvidence(cmd, root, nil)
		if len(ev.Scripts) != 1 || !ev.Complete || !ev.Scripts[0].Complete || ev.Scripts[0].SHA256 == "" || len(requests) != 1 {
			t.Fatalf("%s: %+v", cmd, ev)
		}
		if ev.Scripts[0].Origin != "unknown" {
			t.Fatal("untracked file claimed task ownership")
		}
	}
	ev, _ := collectEvidence(`cd . && timeout 180 bash work/tmp/signing/e2e.sh`, root, nil)
	if !revalidateEvidence(&ev, root) {
		t.Fatal("fresh evidence invalid")
	}
	putFixture(t, root, "work/tmp/signing/e2e.sh", strings.Repeat("# pad\n", 1000)+"security export -k login\n")
	if revalidateEvidence(&ev, root) {
		t.Fatal("change after 4 KiB did not invalidate evidence")
	}
	_ = p
}
func TestApprovalEvidenceBoundsAndSecrets(t *testing.T) {
	needShell(t)
	root := t.TempDir()
	putFixture(t, root, "big.sh", strings.Repeat("# text\n", 4000))
	putFixture(t, root, "binary.sh", "\x00token")
	putFixture(t, root, "secret.sh", "echo API_TOKEN=fixture-secret-value\n")
	for _, name := range []string{"binary.sh", "missing.sh"} {
		ev, _ := collectEvidence("bash "+name, root, nil)
		if ev.Complete || len(ev.Scripts) != 1 || ev.Scripts[0].Failure == "" {
			t.Fatalf("%s: %+v", name, ev)
		}
	}
	ev, _ := collectEvidence("bash big.sh", root, nil)
	if ev.Complete || !ev.Scripts[0].Truncated || ev.Scripts[0].Size <= 16<<10 || len(ev.Scripts[0].Content) > 16<<10 {
		t.Fatalf("bounds %+v", ev)
	}
	ev, _ = collectEvidence("bash secret.sh", root, nil)
	b, _ := json.Marshal(ev)
	if strings.Contains(string(b), "fixture-secret-value") {
		t.Fatal("unmasked script evidence")
	}
	putFixture(t, root, ".ssh/id_ed25519", "private")
	if err := os.Symlink(filepath.Join(root, ".ssh/id_ed25519"), filepath.Join(root, "alias.sh")); err == nil {
		ev, _ = collectEvidence("bash alias.sh", root, nil)
		if ev.Complete {
			t.Fatal("private key alias read")
		}
	}
	for _, cmd := range []string{`bash -c 'echo hi'`, `python3 -m pytest`, `python3 -c 'print(1)'`, `node -e 'console.log(1)'`, `cd $X && bash big.sh`} {
		for _, s := range policy.AnalyzeCommand(cmd, policy.CommandContext{Workdir: root}).Scripts {
			if s.Path == "pytest" || s.Path == "print(1)" || s.Path == "console.log(1)" {
				t.Fatalf("code/module treated as filename: %+v", s)
			}
		}
	}
}
func TestApprovalScriptEffectsReachServerGuard(t *testing.T) {
	root := t.TempDir()
	d := testDaemon(t, t.TempDir(), Config{Role: "server"})
	r := &run{d: d, spec: proto.RunStart{PermissionMode: "auto"}}
	for _, body := range []string{"rm task-file\n", "systemctl restart app\n", "security export -k login\n", "sudo true\n"} {
		putFixture(t, root, "script.sh", body)
		raw, _ := json.Marshal(map[string]string{"command": "bash script.sh"})
		req := policy.Request{ToolKind: "execute", RawInput: raw, Workdir: root}
		c, _ := d.pol().Classify(req)
		_, _, c, a := r.approvalEvidence(req, c)
		act, _ := policy.Judge(policy.RoleServer, policy.ModeAuto, c, a)
		if act != policy.AskUser {
			t.Fatalf("%s got %s %+v", body, act, a)
		}
	}
}
func TestBuiltinSousPermissionSample12(t *testing.T) {
	d := testDaemon(t, t.TempDir(), Config{})
	r := &run{d: d, spec: proto.RunStart{RunID: "r", Purpose: proto.PurposeWork, Agent: "claude"}, ctx: context.Background()}
	d.sousServers(r)
	for title, raw := range map[string]string{"mcp__sous__report_progress": `{"text":"progress"}`, "mcp__sous__report_result": `{"done":"done","verified":"tests"}`, "mcp__sous__ask_supervisor": `{"question":"which port"}`} {
		req := acp.PermissionRequest{}
		req.ToolCall.Title = title
		req.ToolCall.RawInput = json.RawMessage(raw)
		if !r.isBuiltinSousRequest(req) {
			t.Fatalf("not exempt %s", title)
		}
		req.ToolCall.Kind = "execute"
		if r.isBuiltinSousRequest(req) {
			t.Fatal("execute exempted")
		}
		req.ToolCall.Kind = ""
		req.ToolCall.RawInput = json.RawMessage(`{"command":"rm task"}`)
		if r.isBuiltinSousRequest(req) {
			t.Fatal("command exempted")
		}
	}
	for _, title := range []string{"mcp__sous__unknown", "mcp__sous__report_result_extra"} {
		req := acp.PermissionRequest{}
		req.ToolCall.Title = title
		req.ToolCall.RawInput = json.RawMessage(`{"text":"x"}`)
		if r.isBuiltinSousRequest(req) {
			t.Fatal("wildcard exemption")
		}
	}
	r.sous.builtin = false
	req := acp.PermissionRequest{}
	req.ToolCall.Title = "mcp__sous__report_progress"
	req.ToolCall.RawInput = json.RawMessage(`{"text":"x"}`)
	if r.isBuiltinSousRequest(req) {
		t.Fatal("custom/off server exemption")
	}
}
func TestApprovalInspectionScopeAndProjection(t *testing.T) {
	needShell(t)
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	putFixture(t, home, ".zshrc", "export PATH=/tools:$PATH\nexport API_TOKEN=fixture-secret-value\nexport PATH=$(echo stolen)\n")
	putFixture(t, home, ".ssh/config", "Host staging\n HostName internal.example\n User deploy\n Port 2222\n ProxyCommand sh -c 'API_TOKEN=fixture-secret-value'\n IdentityFile ~/.ssh/id_ed25519\n")
	putFixture(t, root, "script.sh", "echo safe\n")
	putFixture(t, home, "Library/Keychains/fixture.keychain-db", "never read keychain contents")
	d := testDaemon(t, t.TempDir(), Config{HubURL: "https://user:password@hub.example/path?token=secret"})
	r := &run{d: d, ctx: context.Background(), workdir: root, approvals: map[string]chan string{"a": make(chan string)}, approvalScopes: map[string]approvalScope{"a": {root: root}}}
	d.runs["r"] = r
	for _, p := range []string{"script.sh", filepath.Join(home, ".zshrc"), filepath.Join(home, ".ssh/config"), filepath.Join(d.root, "node.json")} {
		res, err := d.admin.approvalRead(context.Background(), proto.ApprovalReadArgs{RunID: "r", ApprovalID: "a", Path: p})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if strings.Contains(res.Content, "fixture-secret") || strings.Contains(res.Content, "ProxyCommand") || strings.Contains(res.Content, "IdentityFile") || strings.Contains(res.Content, "password") || strings.Contains(res.Content, "?token") {
			t.Fatalf("projection leak: %s", res.Content)
		}
	}
	for _, p := range []string{filepath.Join(home, ".ssh/id_ed25519"), filepath.Join(home, "unrelated"), ".git/config", "/etc/shadow"} {
		if _, err := d.admin.approvalRead(context.Background(), proto.ApprovalReadArgs{RunID: "r", ApprovalID: "a", Path: p}); err == nil {
			t.Fatalf("unexpected read %s", p)
		}
	}
	if _, err := d.admin.look(context.Background(), proto.LookArgs{Dir: root, Command: "cat ~/.zshrc"}); err == nil {
		t.Fatal("ordinary look got config capability")
	}
	res, err := d.admin.look(context.Background(), proto.LookArgs{RunID: "r", ApprovalID: "a", Command: "cat ~/.zshrc"})
	if err != nil || !strings.Contains(res.Output, "/tools") {
		t.Fatalf("bound projection: %+v %v", res, err)
	}
	res, err = d.admin.look(context.Background(), proto.LookArgs{RunID: "r", ApprovalID: "a", Command: "ls ~/Library/Keychains"})
	if err != nil || !strings.Contains(res.Output, "fixture.keychain-db") || strings.Contains(res.Output, "never read") {
		t.Fatalf("metadata listing: %+v %v", res, err)
	}
	if _, err = d.admin.look(context.Background(), proto.LookArgs{Dir: root, Command: "ls ~/Library/Keychains"}); err == nil {
		t.Fatal("ordinary look got keychain exception")
	}
	if err = os.Symlink(filepath.Join(home, ".zshrc"), filepath.Join(root, "config-alias")); err != nil {
		t.Fatal(err)
	}
	if _, err = d.admin.look(context.Background(), proto.LookArgs{Dir: root, Command: "cat config-alias"}); err == nil {
		t.Fatal("config alias bypassed ordinary look")
	}
	delete(r.approvals, "a")
	if _, err := d.admin.approvalRead(context.Background(), proto.ApprovalReadArgs{RunID: "r", ApprovalID: "a", Path: "script.sh"}); err == nil {
		t.Fatal("closed approval read")
	}
}

func TestLocalApprovalRollbackFlagsCannotBeChangedRemotely(t *testing.T) {
	root := t.TempDir()
	cfg := Config{HubURL: "https://hub.example", NodeID: "fixture-node", Token: "fixture-token", ApprovalInspectDisabled: true, Policy: policy.Config{LegacyShell: true, HardReadDisabled: true}}
	if err := SaveConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	d := testDaemon(t, root, cfg)
	next, err := d.admin.mergeNodeConfig([]byte(`{"approval_inspect_disabled":false,"policy":{"legacy_shell":false,"hard_read_disabled":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !next.ApprovalInspectDisabled || !next.Policy.LegacyShell || !next.Policy.HardReadDisabled {
		t.Fatal("remote edit loosened local flags")
	}
	p, _ := policy.New(next.Policy)
	r := policy.Request{ToolKind: "execute", Title: `grep 'a|b' file`, Workdir: root}
	c, _ := p.Classify(r)
	if c == policy.Read {
		t.Fatal("legacy parser rollback not applied")
	}
}

func TestRedactionPrivateBlocksAndTruncatedJSON(t *testing.T) {
	var s redactedLine
	out := s.push("-----BE", false) + s.push("GIN PRIVATE KEY-----\nprivate-fixture", false) + s.push("-body\n-----END PRIVATE KEY-----\nafter\n", false)
	if strings.Contains(out, "private-fixture") || !strings.Contains(out, "after") {
		t.Fatalf("PEM stream leaked or stalled: %q", out)
	}
	out = s.push(strings.Repeat("x", 17000), false) + s.push("API_TOKEN=fixture-secret\n", false)
	if strings.Contains(out, "fixture-secret") {
		t.Fatal("oversized line leak")
	}
	if got := mask(`{"token":"fixture-secret`); strings.Contains(got, "fixture-secret") {
		t.Fatal("truncated JSON leaked", got)
	}
	if got := s.push("正在验证", false); got != "正在验证" {
		t.Fatal("Chinese progress delayed", got)
	}
}
func TestRedactionAcrossChunks(t *testing.T) {
	var s redactedLine
	a := s.push("API_TO", false)
	a += s.push("KEN=fixture-", false)
	a += s.push("secret-value\nhello", false)
	a += s.push("", true)
	if strings.Contains(a, "fixture-") || !strings.Contains(a, "hello") {
		t.Fatalf("stream: %q", a)
	}
	got := maskJSON(json.RawMessage(`{"api_key":"fixture-secret-value","nested":{"token":"another-secret"}}`))
	if strings.Contains(got, "secret-value") || strings.Contains(got, "another-secret") {
		t.Fatalf("JSON leak %s", got)
	}
}

func TestApprovalIdentityBindsScriptsAndExecutionParameters(t *testing.T) {
	needShell(t)
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, out)
	}
	if err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/task-branch").Run(); err != nil {
		t.Fatal(err)
	}
	rroot, _ := filepath.EvalSymlinks(root)
	d := testDaemon(t, t.TempDir(), Config{NodeID: "node"})
	r := &run{d: d, spec: proto.RunStart{TaskID: "task", Agent: "claude", Branch: "task-branch", PermissionMode: "auto"}}
	r.approvalLaunch = []string{"fixture-agent"}
	r.approvalEnv = os.Environ()
	putFixture(t, root, "script.sh", "echo harmless\n")
	identity := func(command string, timeout int, description string) string {
		raw, _ := json.Marshal(map[string]any{"command": command, "timeout": timeout, "description": description})
		req := policy.Request{ToolKind: "execute", RawInput: raw, Workdir: rroot}
		c, _ := d.pol().Classify(req)
		f, _, _, _ := r.approvalEvidence(req, c)
		return f.OperationKey
	}
	base := identity("bash script.sh", 10, "one")
	if base == "" || identity("bash script.sh", 10, "two") != base {
		t.Fatal("description prevented a complete identity")
	}
	if identity("bash script.sh", 11, "one") == base || identity("bash script.sh 'a  b'", 10, "one") == identity("bash script.sh 'a b'", 10, "one") {
		t.Fatal("timeout/quoted whitespace lost")
	}
	if err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/other-branch").Run(); err != nil {
		t.Fatal(err)
	}
	if identity("bash script.sh", 10, "one") == base {
		t.Fatal("actual branch change not bound")
	}
	if err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/task-branch").Run(); err != nil {
		t.Fatal(err)
	}
	putFixture(t, root, "script.sh", strings.Repeat("# pad\n", 1000)+"echo changed\n")
	if identity("bash script.sh", 10, "one") == base {
		t.Fatal("full script digest not bound")
	}
	r.spec.TaskID = "different"
	if identity("bash script.sh", 10, "one") == base {
		t.Fatal("task not bound")
	}
}
func TestBuiltinSousIsAllowedOnceAndAudited(t *testing.T) {
	d := testDaemon(t, t.TempDir(), Config{})
	r := &run{d: d, ctx: context.Background(), spec: proto.RunStart{RunID: "r", Agent: "claude", Purpose: proto.PurposeWork, PermissionMode: "auto"}}
	d.sousServers(r)
	req := acp.PermissionRequest{Options: []acp.PermissionOption{{OptionID: "once", Kind: "allow_once"}, {OptionID: "always", Kind: "allow_always"}}}
	req.ToolCall.Title = "mcp__sous__report_result"
	req.ToolCall.RawInput = json.RawMessage(`{"done":"finished"}`)
	if got := r.onPermission(context.Background(), req); got.OptionID != "once" {
		t.Fatalf("decision %+v", got)
	}
	d.ob.mu.Lock()
	defer d.ob.mu.Unlock()
	found := false
	for _, f := range d.ob.pending {
		if f.T == proto.TApprovalRequest {
			t.Fatal("builtin notification sent for Hub approval")
		}
		if f.T == proto.TRunUpdate {
			var u proto.RunUpdate
			_ = f.Decode(&u)
			found = found || u.Kind == proto.UpdatePermission && u.Status == "allow"
		}
	}
	if !found {
		t.Fatal("no policy audit")
	}
}

func TestSample09RequiresProofBeforePrivateKeyExport(t *testing.T) {
	needShell(t)
	root := t.TempDir()
	d := testDaemon(t, t.TempDir(), Config{})
	r := &run{d: d, spec: proto.RunStart{PermissionMode: "auto"}}
	for _, tc := range []struct {
		body   string
		action policy.Action
	}{
		{"#!/bin/sh\nif [ -z \"${SOUS_SIGN_CERT_P12_PASSWORD:-}\" ]; then exit 1; fi\nsecurity export -k login\n", policy.AskAI},
		{"#!/bin/sh\nsecurity export -k login\n", policy.AskUser},
		{"#!/bin/sh\necho before\nif [ -z \"${SOUS_SIGN_CERT_P12_PASSWORD:-}\" ]; then exit 1; fi\nsecurity export -k login\n", policy.AskUser},
	} {
		putFixture(t, root, "export.sh", tc.body)
		raw, _ := json.Marshal(map[string]string{"command": "env -u SOUS_SIGN_CERT_P12_PASSWORD sh export.sh"})
		req := policy.Request{ToolKind: "execute", RawInput: raw, Workdir: root}
		c, _ := d.pol().Classify(req)
		_, ev, c, a := r.approvalEvidence(req, c)
		act, _ := policy.Judge(policy.RoleDev, policy.ModeAuto, c, a)
		if act != tc.action {
			t.Fatalf("got %s want %s: %+v %+v", act, tc.action, a, ev)
		}
	}
	putFixture(t, root, "export.sh", "#!/bin/sh\nif [ -z \"${SOUS_SIGN_CERT_P12_PASSWORD:-}\" ]; then exit 1; fi\nsecurity export -k login\n")
	for _, command := range []string{"env -u SOUS_SIGN_CERT_P12_PASSWORD BASH_ENV=hook bash export.sh", "export BASH_ENV=hook; env -u SOUS_SIGN_CERT_P12_PASSWORD sh export.sh"} {
		raw, _ := json.Marshal(map[string]string{"command": command})
		req := policy.Request{ToolKind: "execute", RawInput: raw, Workdir: root}
		c, _ := d.pol().Classify(req)
		_, _, c, a := r.approvalEvidence(req, c)
		act, _ := policy.Judge(policy.RoleDev, policy.ModeAuto, c, a)
		if act != policy.AskUser {
			t.Fatalf("shell hook bypassed export scrutiny: %s => %s", command, act)
		}
	}
}

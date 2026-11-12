package node

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp"
)

// no_action records the absence of operations without failing the execution;
// explicit provider errors still fail (end-to-end review 4.1).
func TestOutcome(t *testing.T) {
	cases := []struct {
		calls             int
		stat, summary     string
		status, kind, out string
	}{
		{0, "", "Upgrade your plan to continue", "failed", proto.ErrAgentQuota, ""},
		{0, "", "You've reached your monthly usage limit", "failed", proto.ErrAgentQuota, ""},
		{0, "", "Rate limit exceeded, try again later", "failed", proto.ErrAgentRateLimit, ""},
		{0, "", "好的，我看了一下。", "succeeded", "", proto.OutcomeNoAction},
		{0, "", "", "succeeded", "", proto.OutcomeNoAction},
		{2, "", "Added rate limiting to the API", "succeeded", "", ""},
		{0, "1 file changed", "Upgrade your plan to continue", "succeeded", "", ""},
		{0, "", strings.Repeat("这是一个很长的回答。", 40), "succeeded", "", ""},
	}
	for _, c := range cases {
		st, kind, out := outcome(c.calls, c.stat, c.summary)
		if st != c.status || kind != c.kind || out != c.out {
			t.Errorf("outcome(%d, %q, %q) = %s %s %s, want %s %s %s", c.calls, c.stat, c.summary, st, kind, out, c.status, c.kind, c.out)
		}
	}
}

func TestErrorKind(t *testing.T) {
	cases := map[string]string{
		`acp error -32603: {"errorType":"UsageLimitReachedError"}`: proto.ErrAgentQuota,
		"429 Too Many Requests":           proto.ErrAgentRateLimit,
		"Service temporarily unavailable": proto.ErrAgentUnavailable,
		"please log in first":             proto.ErrAgentNotLoggedIn,
		"exit status 1":                   "",
	}
	for text, want := range cases {
		if got := errorKind(errors.New(text), ""); got != want {
			t.Errorf("errorKind(%q) = %q, want %q", text, got, want)
		}
	}
	if got := errorKind(acp.ErrNotLoggedIn, ""); got != proto.ErrAgentNotLoggedIn {
		t.Errorf("ErrNotLoggedIn: %q", got)
	}
}

// A tool's output goes to the run's log with secrets masked.
func TestToolOutputMasksSecrets(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "failed",
		"content": []map[string]any{{"type": "content", "content": map[string]string{"type": "text",
			"text": "fatal: could not read from https://me:ghp_abcdefghijklmnopqrstuvwxyz0123@github.com/x.git\nAPI_KEY=sk-live-1234567890abcdef\nexit 128"}}},
	})
	out := toolOutput(raw)
	if !strings.Contains(out, "fatal: could not read") || !strings.Contains(out, "exit 128") {
		t.Fatalf("output lost: %q", out)
	}
	if strings.Contains(out, "ghp_abcdefghij") || strings.Contains(out, "sk-live-1234") {
		t.Fatalf("secret leaked: %q", out)
	}
	raw, _ = json.Marshal(map[string]any{"rawOutput": map[string]any{"stdout": "ok", "exit_code": 2}})
	if out := toolOutput(raw); !strings.Contains(out, "ok") || !strings.Contains(out, "exit code: 2") {
		t.Fatalf("raw output: %q", out)
	}
}

// The approval of a script shows the script, and only scripts inside the
// working directory (end-to-end review 4.7) or the temporary directory,
// also when an interpreter runs it (decision 0024).
func TestScriptPreview(t *testing.T) {
	needShell(t)
	dir := t.TempDir()
	tmp := t.TempDir()
	defer func(f func() string) { scriptTempDir = f }(scriptTempDir)
	scriptTempDir = func() string { return tmp }
	os.WriteFile(filepath.Join(tmp, "check.mjs"), []byte("console.log('checking')\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "tool.py"), []byte("print('tool')\n"), 0o644)
	if got := scriptPreview("node "+filepath.Join(tmp, "check.mjs"), dir); !strings.Contains(got, "checking") {
		t.Fatalf("a script in the temporary directory: %q", got)
	}
	if got := scriptPreview("python3 -u tool.py --x", dir); !strings.Contains(got, "print('tool')") {
		t.Fatalf("a python script: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\ngit pull\nexport TOKEN=abc123secretvalue\nsystemctl restart app\n"), 0o755)
	outside := filepath.Join(t.TempDir(), "evil.sh")
	os.WriteFile(outside, []byte("rm -rf /\n"), 0o755)
	for _, cmd := range []string{"./deploy.sh", "bash deploy.sh", "sh -e ./deploy.sh --fast"} {
		got := scriptPreview(cmd, dir)
		if !strings.Contains(got, "systemctl restart app") || strings.Contains(got, "abc123secretvalue") {
			t.Fatalf("%s: %q", cmd, got)
		}
	}
	for _, cmd := range []string{"bash " + outside, "./missing.sh", "ls -la", "bash ../x.sh", "./deploy.sh $(whoami)x"} {
		if got := scriptPreview(cmd, dir); got != "" && cmd != "./deploy.sh $(whoami)x" {
			t.Fatalf("%s: %q", cmd, got)
		}
	}
}

// Four failures in a row of the same tool call are reported once; a success
// resets the count (end-to-end review 4.16).
func TestTrackFailure(t *testing.T) {
	r := &run{toolTitles: map[string]string{}}
	r.toolTitles["a"] = "Shell"
	var reports []string
	for i := 0; i < 6; i++ {
		if s := r.trackFailure("a", "", "failed"); s != "" {
			reports = append(reports, s)
		}
	}
	if len(reports) != 1 || !strings.Contains(reports[0], "Shell") || !strings.Contains(reports[0], "4 次") {
		t.Fatalf("reports %q", reports)
	}
	r.trackFailure("b", "Read", "completed")
	for i := 0; i < 3; i++ {
		if s := r.trackFailure("a", "Shell", "failed"); s != "" {
			t.Fatalf("reported after a success reset the count: %q", s)
		}
	}
}

// project_read cannot reach .git under another spelling, nor read a FIFO
// (end-to-end review M1, L2).
func TestProjectPathRefusesGitAliases(t *testing.T) {
	ws := t.TempDir()
	root := filepath.Join(ws, "demo")
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://x:tok@example.com/r.git\n"), 0o644)
	a := &admin{d: &Daemon{cfg: Config{Workspaces: []string{ws}}}}
	for _, rel := range []string{".git/config", ".GIT/config", ".Git/config", ".git./config", "GIT~1/config", "src/../.git/config"} {
		if _, err := a.projectOp(proto.OpProjectRead, proto.ProjectArgs{Root: root, Rel: rel}); err == nil {
			t.Errorf("read %s", rel)
		}
	}
}

// Execution success, work evidence, and task acceptance must not be conflated.
func TestWorkOutcomeSeparatesConnectionAndEvidence(t *testing.T) {
	for _, text := range []string{"测试通过", "服务已启动", "任务已完成了一部分", "主体工作已完成一半"} {
		if completedProgressRE.MatchString(text) {
			t.Fatalf("intermediate progress mistaken for completion: %q", text)
		}
	}
	for _, tc := range []struct {
		name, status       string
		changed, completed bool
		result             *sousmcp.Call
		want, outcome      string
	}{
		{"normal-result", "succeeded", false, false, &sousmcp.Call{Done: "完成"}, "succeeded", proto.OutcomeWorkProduced},
		{"normal-changes", "succeeded", true, false, nil, "succeeded", proto.OutcomeWorkProduced},
		{"normal-no-result", "succeeded", false, false, nil, "succeeded", ""},
		{"acp-error-after-result", "failed", false, false, &sousmcp.Call{Done: "完成"}, "interrupted", proto.OutcomeNeedsConfirmation},
		{"acp-error-without-output", "failed", false, false, nil, "failed", ""},
		{"disconnect-after-changes", "failed", true, false, nil, "interrupted", proto.OutcomeNeedsConfirmation},
		{"disconnect-after-completed-progress", "failed", false, true, nil, "interrupted", proto.OutcomeNeedsConfirmation},
		{"refusal-cancel-without-output", "failed", false, false, nil, "failed", ""},
		{"explicit-failure-with-changes", "succeeded", true, true, &sousmcp.Call{Done: "执行结果", Status: "failed"}, "failed", ""},
		{"explicit-failure-after-acp-error", "failed", true, true, &sousmcp.Call{Done: "失败：验证未通过"}, "failed", ""},
		{"legacy-deploy-failure", "succeeded", false, false, &sousmcp.Call{Done: "部署失败，服务未能启动"}, "failed", ""},
		{"legacy-english-failure", "succeeded", true, false, &sousmcp.Call{Done: "Failed to start service"}, "failed", ""},
		{"cancel-with-output", "cancelled", true, true, &sousmcp.Call{Done: "完成"}, "cancelled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.status != "succeeded" {
				err = errors.New("stream disconnected before completion")
			}
			got := workOutcome(tc.status, "", "执行摘要", "old task diff", err, 2, tc.changed, tc.result, tc.completed)
			if got.Status != tc.want || got.Outcome != tc.outcome {
				t.Fatalf("got %+v; want %s / %s", got, tc.want, tc.outcome)
			}
		})
	}
	got := workOutcome("succeeded", "end_turn", "好的", "old task diff", nil, 0, false, nil, false)
	if got.Status != "succeeded" || got.Outcome != proto.OutcomeNoAction {
		t.Fatalf("old diff treated as this run's work: %+v", got)
	}
	got = workOutcome("succeeded", "end_turn", "Upgrade your plan to continue", "", nil, 0, false, nil, false)
	if got.Status != "failed" {
		t.Fatalf("real error hidden: %+v", got)
	}
	if resultFailed(&sousmcp.Call{Done: "修复失败：的显示文案", Verified: "之前测试失败，已修复", Left: "无"}) {
		t.Fatal("verification prose or a mid-sentence word was treated as a task verdict")
	}
}

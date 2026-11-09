package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

// How an agent says it cannot go on. Agents report these as errors (ACP
// -32603 with the provider's message) or, some of them, as the only text
// of a turn that "ended normally".
var (
	quotaRE             = regexp.MustCompile(`(?i)usage ?limit|quota|upgrade your plan|out of credits|insufficient (credits|balance|funds)|credit balance|billing|monthly limit|plan limit|UsageLimitReached|exceeded your (current )?(plan|limit|usage)`)
	rateLimitRE         = regexp.MustCompile(`(?i)rate.?limit|too many requests|\b429\b`)
	notLoggedInRE       = regexp.MustCompile(`(?i)not logged in|log ?in (is )?required|please (log|sign) ?in|unauthenticated|\b401\b|invalid api key|authentication (failed|required)`)
	unavailableRE       = regexp.MustCompile(`(?i)service (is )?(temporarily )?unavailable|overloaded|\b50[234]\b|bad gateway|internal server error`)
	resultFailureRE     = regexp.MustCompile(`(?i)^(?:(?:任务|部署|执行)?(?:失败|未完成|没有完成)|(?:无法|未能)完成(?:任务)?|failed)(?:$|[\s：:，,。.!！])`)
	completedProgressRE = regexp.MustCompile(`^(?:主体工作已完成|任务已完成)(?:$|[\s：:，,。.!！])`)
)

// errorKind classifies why an agent stopped, from its error or its words.
func errorKind(err error, text string) string {
	if errors.Is(err, acp.ErrNotLoggedIn) {
		return proto.ErrAgentNotLoggedIn
	}
	if err != nil {
		text = err.Error() + "\n" + text
	}
	switch {
	case quotaRE.MatchString(text):
		return proto.ErrAgentQuota
	case rateLimitRE.MatchString(text):
		return proto.ErrAgentRateLimit
	case notLoggedInRE.MatchString(text):
		return proto.ErrAgentNotLoggedIn
	case unavailableRE.MatchString(text):
		return proto.ErrAgentUnavailable
	}
	return ""
}

// noActionRunes bounds the words of a turn that did nothing: a longer
// answer is an answer (a question needs no tool), a short one is a refusal
// or an error message.
const noActionRunes = 300

// outcome describes a normally ended work run. no_action is an observation
// for acceptance, not a task failure. Existing provider error detection stays
// intact for turns that only report a quota, rate-limit or login error.
func outcome(toolCalls int, diffstat, summary string) (status, kind, out string) {
	if toolCalls > 0 || diffstat != "" || utf8.RuneCountInString(strings.TrimSpace(summary)) > noActionRunes {
		return "succeeded", "", ""
	}
	switch k := errorKind(nil, summary); k {
	case proto.ErrAgentQuota, proto.ErrAgentRateLimit, proto.ErrAgentNotLoggedIn:
		return "failed", k, ""
	}
	return "succeeded", "", proto.OutcomeNoAction
}

// workOutcome keeps execution, evidence and acceptance separate. A refused
// command is not a task verdict: only explicit failure/no-output termination
// fails a run. Reports, an explicit completion declaration, or files changed
// since THIS run began preserve ambiguous endings for user confirmation.
// Tool counts, summary length and a diff from an older run are not such proof.
func workOutcome(status, stop, summary, stat string, err error, calls int, changed bool, result *sousmcp.Call, completed bool) proto.RunEnded {
	e := proto.RunEnded{Status: status, StopReason: stop, Summary: summary, DiffStat: stat}
	if err != nil {
		e.Error = err.Error()
	}
	if status == "cancelled" { // a user cancellation always wins
		return e
	}
	if resultFailed(result) {
		e.Status, e.Error = "failed", "agent 报告任务失败："+result.Done
		return e
	}
	produced := changed || result != nil || completed
	if status == "failed" || status == "interrupted" {
		if produced {
			e.Status, e.Outcome = "interrupted", proto.OutcomeNeedsConfirmation
		} else if status == "failed" {
			e.ErrorKind = errorKind(err, "")
		}
		return e
	}
	if produced {
		e.Outcome = proto.OutcomeWorkProduced
		return e
	}
	e.Status, e.ErrorKind, e.Outcome = outcome(calls, "", summary)
	if e.Status == "failed" {
		e.Error = "agent 没做任何操作就结束了，它说：" + truncate(summary, 300)
	}
	return e
}

func (r *run) hasWorkEvidence(ws workspace.Prepared) bool {
	r.mu.Lock()
	reported := r.sous.result != nil || r.sous.completed
	baseline := r.workBaseline
	r.mu.Unlock()
	if reported {
		return true
	}
	if baseline == "" {
		return false
	}
	current, err := workspace.Fingerprint(r.ctx, ws)
	return err == nil && current != "" && current != baseline
}

func resultFailed(c *sousmcp.Call) bool {
	if c == nil {
		return false
	}
	if c.Status == "failed" {
		return true
	}
	// Compatibility for older report_result callers: recognize only an
	// explicit leading verdict, never words in verification/risk prose (a
	// fixed test failure is not a failed task). New callers use status=failed.
	return resultFailureRE.MatchString(strings.TrimSpace(c.Done))
}

// outputRunes bounds a tool's output in the run's log.
const outputRunes = 2000

// toolOutput is the text a finished tool call returned, from its content
// blocks or its rawOutput, cut short and masked.
func toolOutput(raw json.RawMessage) string {
	var u struct {
		Content []struct {
			Type    string `json:"type"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Text string `json:"text"`
		} `json:"content"`
		RawOutput json.RawMessage `json:"rawOutput"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range u.Content {
		t := c.Content.Text
		if t == "" {
			t = c.Text
		}
		if t = strings.TrimSpace(t); t != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(t)
		}
	}
	if b.Len() == 0 && len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
		var s string
		if json.Unmarshal(u.RawOutput, &s) == nil {
			b.WriteString(s)
		} else {
			var obj map[string]any
			if json.Unmarshal(u.RawOutput, &obj) == nil {
				for _, k := range []string{"output", "stdout", "stderr", "error", "formatted_output", "aggregated_output"} {
					if v, ok := obj[k].(string); ok && strings.TrimSpace(v) != "" {
						if b.Len() > 0 {
							b.WriteString("\n")
						}
						b.WriteString(strings.TrimSpace(v))
					}
				}
				if v, ok := obj["exit_code"]; ok {
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString("exit code: " + jsonText(v))
				}
			}
		}
	}
	return truncate(mask(strings.TrimSpace(b.String())), outputRunes)
}

func jsonText(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// mask hides what looks like a secret in text that goes to the Hub's logs.
func mask(s string) string { return redact.Mask(s) }

// interpreters run the script file named by their first argument.
var interpreters = map[string]bool{
	"node": true, "python": true, "python3": true, "deno": true, "bun": true, "ruby": true, "perl": true,
	"tsx": true, "ts-node": true, "php": true, "pwsh": true, "powershell": true,
}

// scriptTempDir is where agents keep scratch scripts (tests replace it).
var scriptTempDir = os.TempDir

// scriptBytes bounds the script shown with an approval.
const scriptBytes = 4096

// scriptPreview is the beginning of the script a command runs, when it runs
// one from the working directory (./deploy.sh, bash scripts/x.sh): the user
// and the approver see what will run, not only its name (end-to-end review
// 4.7). Only files inside workdir or the temporary directory are read
// (decision 0024), and only text.
func scriptPreview(cmd, workdir string) string {
	ev, _ := collectEvidence(cmd, workdir, nil)
	var parts []string
	for _, s := range ev.Scripts {
		if s.Failure != "" {
			continue
		}
		text := s.Content
		if len(text) > scriptBytes {
			text = text[:scriptBytes]
		}
		head := "脚本 " + s.Path + " 的内容"
		if s.Size > scriptBytes {
			head += fmt.Sprintf("（共 %d 字节，这里是前 %d 字节）", s.Size, scriptBytes)
		}
		parts = append(parts, head+"：\n"+text)
	}
	return strings.Join(parts, "\n\n")
}

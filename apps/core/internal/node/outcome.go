package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
)

// How an agent says it cannot go on. Agents report these as errors (ACP
// -32603 with the provider's message) or, some of them, as the only text
// of a turn that "ended normally".
var (
	quotaRE       = regexp.MustCompile(`(?i)usage ?limit|quota|upgrade your plan|out of credits|insufficient (credits|balance|funds)|credit balance|billing|monthly limit|plan limit|UsageLimitReached|exceeded your (current )?(plan|limit|usage)`)
	rateLimitRE   = regexp.MustCompile(`(?i)rate.?limit|too many requests|\b429\b`)
	notLoggedInRE = regexp.MustCompile(`(?i)not logged in|log ?in (is )?required|please (log|sign) ?in|unauthenticated|\b401\b|invalid api key|authentication (failed|required)`)
	unavailableRE = regexp.MustCompile(`(?i)service (is )?(temporarily )?unavailable|overloaded|\b50[234]\b|bad gateway|internal server error`)
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

// outcome judges a work run that ended its turn normally. A turn without
// a single tool call, without a change and with only a few words did no
// work: it is reported as such instead of as done (report 4.1). When the
// words say the agent is out of its plan or rate limited, the run failed.
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
	if workdir == "" {
		return ""
	}
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return ""
	}
	path := ""
	switch base := filepath.Base(f[0]); {
	case base == "sh" || base == "bash" || base == "zsh" || base == "dash" || base == "fish" || interpreters[base]:
		for _, a := range f[1:] {
			if !strings.HasPrefix(a, "-") {
				path = a
				break
			}
		}
	case strings.HasPrefix(f[0], "./") || strings.HasSuffix(f[0], ".sh") || strings.HasSuffix(f[0], ".bash"):
		path = f[0]
	}
	path = strings.Trim(path, `"'`)
	if path == "" || strings.ContainsAny(path, "$`;&|<>") {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workdir, path)
	}
	abs = filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	// The working directory, or the system's temporary directory where
	// agents keep their own scratch scripts.
	inside := false
	for _, dir := range []string{workdir, scriptTempDir()} {
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			root = dir
		}
		if rel, err := filepath.Rel(root, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			inside = true
		}
	}
	if !inside {
		return ""
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	fh, err := os.Open(real)
	if err != nil {
		return ""
	}
	defer fh.Close()
	buf := make([]byte, scriptBytes)
	n, _ := io.ReadFull(fh, buf)
	buf = buf[:n]
	if bytes.IndexByte(buf, 0) >= 0 {
		return ""
	}
	for len(buf) > 0 && !utf8.Valid(buf) {
		buf = buf[:len(buf)-1]
	}
	text := mask(string(buf))
	head := "脚本 " + path + " 的内容"
	if fi.Size() > scriptBytes {
		head += fmt.Sprintf("（共 %d 字节，这里是前 %d 字节）", fi.Size(), scriptBytes)
	}
	return head + "：\n" + text
}

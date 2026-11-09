package policy

import (
	"path/filepath"
	"strings"
)

func appendUnique(xs []string, s string) []string {
	for _, v := range xs {
		if v == s {
			return xs
		}
	}
	return append(xs, s)
}
func (p *Policy) Assess(r Request, cat Category) Assessment {
	r = ResolveRequest(r)
	a := p.assessCommand(r, cat)
	// Declared file effects also apply when a command/path category masks them.
	switch r.ToolKind {
	case "edit", "move":
		a.Effects = append(a.Effects, EditWorktree)
	case "delete":
		a.Effects = append(a.Effects, Delete)
	}
	return a
}
func (p *Policy) assessCommand(r Request, cat Category) Assessment {
	if p.legacyShell {
		a := p.assessLegacy(r, cat)
		a.ParseStatus = "legacy"
		a.Opaque = true
		return a
	}
	fullCmd := CommandOf(r)
	cmd := ReachableShell(fullCmd, r.UnsetEnv)
	if cmd == "" {
		a := p.assessLegacy(r, cat)
		switch r.ToolKind {
		case "edit", "move":
			a.Effects = []Category{EditWorktree}
		case "delete":
			a.Effects = []Category{Delete}
		}
		return a
	}
	x := AnalyzeCommand(cmd, CommandContext{r.Workdir, r.CWD, r.Shell})
	if x.Status != "ok" {
		a := p.assessLegacy(r, cat)
		a.ParseStatus = x.Status
		a.Opaque = true
		return a
	}
	a := Assessment{Effects: x.Effects, Opaque: x.Opaque, ParseStatus: x.Status}
	in := parseInput(r.RawInput)
	raw := fullCmd + "\n" + in.floorText + "\n" + strings.Join(append(r.Paths, in.paths...), "\n")
	for _, re := range p.extraHard {
		if re.MatchString(raw) {
			a.Hard = appendUnique(a.Hard, "命中自定义高风险规则 "+re.String())
		}
	}
	texts := []CommandView{}
	texts = append(texts, x.Commands...)
	for _, path := range append(r.Paths, parseInput(r.RawInput).paths...) {
		texts = append(texts, CommandView{SignalText: path, Category: cat})
	}
	for _, v := range texts {
		text := v.SignalText
		name := ""
		if len(v.Argv) > 0 {
			name = filepath.Base(v.Argv[0])
		}
		if name == "git" {
			for _, arg := range v.Argv[1:] {
				if arg == "-c" || strings.HasPrefix(arg, "--config-env") || strings.HasPrefix(arg, "--exec-path") {
					a.Hard = appendUnique(a.Hard, "Git 执行配置覆盖")
				}
			}
		}
		for i, s := range hardSignals {
			if i == 0 && name == "security" && len(v.Argv) > 1 && oneOf(v.Argv[1], "export", "dump-keychain", "find-generic-password", "find-internet-password") {
				a.Hard = appendUnique(a.Hard, s.name)
			}
			if i == 2 && name != "ssh" {
				continue
			}
			if i == 4 && publicObservation(v) {
				continue
			}
			if m := s.re.FindString(text); m != "" {
				if (i == 0 || i == 4) && !p.noHardRead && x.ReadOnly && sensitiveConfigOrMetadata(v) {
					a.HardRead = appendUnique(a.HardRead, s.name)
					continue
				}
				a.Hard = appendUnique(a.Hard, s.name)
				a.hit(s.name, m)
			}
		}
		// Genuine secrets never become HardRead, including real .env files.
		if secretPathText(text) {
			a.Hard = appendUnique(a.Hard, "秘密内容读取")
		}
		if v.Category == Install && installSource.re.MatchString(text) {
			a.Hard = appendUnique(a.Hard, installSource.name)
		}
		if b := pushedProtectedArgv(v.Argv); b != "" {
			a.Hard = appendUnique(a.Hard, "push 到受保护的分支 "+b)
		}
		for _, s := range softSignals {
			if s.re.MatchString(text) && !(s.name == "读写 .env 文件" && envFileExample.MatchString(text)) {
				a.Soft = appendUnique(a.Soft, s.name)
				a.hit(s.name, s.re.FindString(text))
			}
		}
	}
	if len(a.Hard) == 0 && len(a.HardRead) == 0 && !p.noRoutine {
		a.Routine = routine(cmd, r.Workdir, cat)
	}
	return a
}
func publicObservation(v CommandView) bool {
	if v.Category != Read || len(v.Argv) == 0 {
		return false
	}
	if readOnlyInvocation(v.Argv) {
		return true
	}
	name := filepath.Base(v.Argv[0])
	if name != "cat" && name != "ls" && name != "grep" && name != "head" && name != "stat" {
		return false
	}
	return strings.Contains(v.SignalText, "/etc/crontab") || strings.Contains(v.SignalText, "/etc/cron.d")
}
func sensitiveConfigOrMetadata(v CommandView) bool {
	if len(v.Argv) == 0 {
		return false
	}
	name := filepath.Base(v.Argv[0])
	text := v.SignalText
	if name == "ls" || name == "stat" {
		if strings.Contains(text, "Library/Keychains") {
			return true
		}
	}
	if name != "cat" && name != "grep" && name != "rg" && name != "head" && name != "sed" {
		return false
	}
	found := false
	for _, arg := range v.Argv[1:] {
		if strings.HasSuffix(arg, "/.ssh/config") || strings.HasSuffix(arg, "/.zshrc") || strings.HasSuffix(arg, "/.bashrc") || strings.HasSuffix(arg, "/.zprofile") || strings.HasSuffix(arg, "/.profile") {
			found = true
		}
		if secretPathText(arg) {
			return false
		}
	}
	return found
}
func secretPathText(s string) bool {
	l := strings.ToLower(s)
	for _, needle := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "/etc/shadow", "/etc/gshadow", "/.aws/", "/.gnupg/", "/.sous/node.json", "outbox.jsonl", "/.netrc", "/.git-credentials"} {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return softSignals[1].re.MatchString(s) && !envFileExample.MatchString(s)
}
func pushedProtectedArgv(f []string) string {
	if len(f) < 2 || filepath.Base(f[0]) != "git" {
		return ""
	}
	i := 1
	for i < len(f) && (f[i] == "-c" || f[i] == "-C") {
		i += 2
	}
	if i >= len(f) || f[i] != "push" {
		return ""
	}
	pos := 0
	for _, v := range f[i+1:] {
		if v == "--all" || v == "--tags" {
			return "（--all / --tags 会推送全部）"
		}
		if strings.HasPrefix(v, "-") {
			continue
		}
		if pos > 0 && isProtectedBranch(v) {
			return v
		}
		pos++
	}
	return ""
}

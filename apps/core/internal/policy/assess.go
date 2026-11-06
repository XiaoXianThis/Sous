package policy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Assessment is a second look at a request the category alone cannot
// judge (docs/decisions/0019). It never makes a request more permitted
// than the mode table does, except for Routine in the auto mode.
type Assessment struct {
	// Hard are signals no AI may wave through: the request goes to the
	// user (and in the default mode, even an otherwise allowed one does).
	Hard []string
	// Soft are facts the approver agent should weigh.
	Soft []string
	// Routine marks a well-known, bounded action the auto mode allows
	// without calling the approver.
	Routine string
}

// Note is the assessment as text for the approval card and the approver.
func (a Assessment) Note() string {
	var parts []string
	if len(a.Hard) > 0 {
		parts = append(parts, "高风险信号："+strings.Join(a.Hard, "、"))
	}
	if len(a.Soft) > 0 {
		parts = append(parts, "需要留意："+strings.Join(a.Soft, "、"))
	}
	return strings.Join(parts, "\n")
}

type signal struct {
	name string
	re   *regexp.Regexp
}

func sig(name, pattern string) signal {
	return signal{name, regexp.MustCompile(pattern)}
}

// home matches the home directory as an agent may write it.
const home = `(?:~|\$HOME|\$\{HOME\}|/Users/[^/\s"']+|/home/[^/\s"']+|/root)`

// hardSignals are what the approver agent may never decide. They are
// matched on the whole command text, so a false positive only costs the
// user a click, while a miss would let an AI decide something it must not.
var hardSignals = []signal{
	sig("读写凭据或密钥", `(?i)`+home+`/\.(ssh|aws|gnupg|kube|netrc|git-credentials|npmrc|pypirc|docker/config\.json|config/(gcloud|gh)|azure)\b`+
		`|\b(id_rsa|id_ed25519|id_ecdsa)\b|/etc/(shadow|sudoers)|Library/Keychains|\bsecurity\s+(find|dump|export|add)-`),
	sig("向外发送数据", `(?is)\bcurl\b.*(\s-d\b|\s--data\S*|\s-F\b|\s--form\b|\s-T\b|\s--upload-file|\s--json\b|\s-X\s*(POST|PUT|PATCH|DELETE)\b)`+
		`|\bwget\b.*--(post-data|post-file|body-data|body-file)`+
		`|\b(scp|sftp|ftp|nc|ncat|netcat|socat|telnet)\b|/dev/(tcp|udp)/`+
		`|\brsync\b[^|;&]*\s\S*:\S`+
		`|\b(aws\s+s3\s+(cp|sync|mv)|gsutil\s+(cp|rsync|mv)|rclone|az\s+storage)\b`+
		`|\b(Invoke-WebRequest|Invoke-RestMethod|iwr|irm)\b.*-(Method\s+(Post|Put)|Body|InFile)`),
	sig("远程登录", `(^|[\s;&|(])ssh\s+\S`),
	sig("混淆或下载后执行", `(?is)\b(base64|openssl\s+enc|xxd)\b[^|;&]*(-d\b|--decode|-r\b).*\|\s*(sudo\s+)?\S*\b(sh|bash|zsh|python\d?|node|perl|ruby)\b`+
		`|\beval\b|\b(ba|z|da)?sh\s+<\(|\$\(\s*(curl|wget)\b|\x60\s*(curl|wget)\b`+
		`|\b(python\d?|node|perl|ruby)\s+-[ce]\b.*(exec\(|eval\(|urlopen|requests\.|socket|subprocess|child_process|\bsystem\()`+
		`|(?i:-enc(odedcommand)?\b|\biex\b|Invoke-Expression|DownloadString|DownloadFile)`),
	sig("持久化（定时任务、启动项、shell 配置）", `(?i)\b(crontab|schtasks|visudo)\b|\blaunchctl\s+(load|bootstrap|submit|enable)\b|\bsystemctl\s+(enable|link|mask)\b`+
		`|\breg\s+add\b|(\.bashrc|\.zshrc|\.zprofile|\.zshenv|\.bash_profile|\.profile|authorized_keys|LaunchAgents|LaunchDaemons|/etc/cron|/etc/profile)\b`),
	sig("改动 git 的远端、钩子或全局配置", `\bgit\s+remote\s+(add|set-url|rename|remove|rm|set-head)\b`+
		`|\bgit\s+config\b[^|;&]*(--global|--system|core\.(hooksPath|sshCommand|fsmonitor|editor|pager)|credential\.|url\.\S*insteadOf|alias\.|\bremote\.\S*\.url)`+
		`|\.git/(hooks|config)\b|\bGIT_SSH_COMMAND\b|\bgit\s+(-c\s+\S+\s+)*push\b[^|;&]*(://|\S@\S+:)`),
	sig("绕过检查（跳过校验、关闭 TLS 校验）", `(?i)--no-verify\b|\bcurl\b.*(\s-k\b|--insecure)|--no-check-certificate|--trusted-host`+
		`|NODE_TLS_REJECT_UNAUTHORIZED\s*=\s*0|GIT_SSL_NO_VERIFY|strict-ssl\s+false|http\.sslVerify\s*=?\s*false`),
	sig("提权或改权限", `\b(chown|chgrp|setcap|setfacl)\b|\bchmod\b[^|;&]*\+s\b`),
	sig("特权容器", `\bdocker\b[^|;&]*(--privileged|--pid=host|--net=host|--network=host|docker\.sock|-v\s+/:|--mount[^|;&]*src=/(\s|,|$))`),
	sig("改动 GitHub 上的权限或密钥，或合并 PR", `\bgh\s+(secret|auth|ssh-key|gpg-key|variable|ruleset|codespace)\b|\bgh\s+pr\s+merge\b`+
		`|\bgh\s+api\b[^|;&]*(-X\s*(POST|PUT|PATCH|DELETE)\b|--method\s+(POST|PUT|PATCH|DELETE)\b|\s-[fF]\s)`),
}

// installSource is a package source other than the default registry.
var installSource = sig("从非默认来源安装（网址、git、自定义源）",
	`(?i)git\+|https?://|\bgithub:|\bfile:|--git\b|--index-url|--extra-index-url|(^|\s)-i\s+\S|--registry|\.tgz\b|\.whl\b|\.tar\.gz\b`)

var softSignals = []signal{
	sig("联网下载", `\b(curl|wget|Invoke-WebRequest|iwr)\b`),
	sig("读写 .env 文件", `(^|[\s/="'])\.env(\.[\w-]+)?($|[\s"'])`),
	sig("批量或递归操作", `\s-[a-zA-Z]*[rR]\b|--recursive|\s\*|\bxargs\b|\bfind\b[^|;&]*-(delete|exec|execdir)\b`),
	sig("强制或丢弃改动", `--force\b|\s-f\b|reset\s+--hard|\bgit\s+clean\b|checkout\s+--\s`),
	sig("后台常驻进程", `\bnohup\b|\bdisown\b|&\s*$`),
}

// envFileExample is a template, not a secret.
var envFileExample = regexp.MustCompile(`\.env\.(example|sample|template|dist)\b`)

// protectedBranches are branches an agent never pushes to on its own.
var protectedBranches = map[string]bool{
	"main": true, "master": true, "develop": true, "development": true, "trunk": true,
	"release": true, "production": true, "prod": true, "stable": true,
}

func isProtectedBranch(b string) bool {
	b = strings.TrimPrefix(strings.TrimPrefix(b, "+"), "refs/heads/")
	if i := strings.LastIndex(b, ":"); i >= 0 {
		b = b[i+1:]
	}
	b = strings.TrimPrefix(b, "refs/heads/")
	return protectedBranches[b] || strings.HasPrefix(b, "release/") || strings.HasPrefix(b, "hotfix/")
}

// Assess looks at a classified request. cmd is the command line ("" for a
// file tool); paths are the files it names.
func (p *Policy) Assess(r Request, cat Category) Assessment {
	in := parseInput(r.RawInput)
	cmd := in.command
	if cmd == "" && r.ToolKind == "execute" {
		cmd = r.Title
	}
	text := cmd
	for _, path := range append(append([]string{}, r.Paths...), in.paths...) {
		text += "\n" + path
	}
	var a Assessment
	if strings.TrimSpace(text) == "" {
		return a
	}
	for _, s := range hardSignals {
		if s.re.MatchString(text) {
			a.Hard = append(a.Hard, s.name)
		}
	}
	for _, re := range p.extraHard {
		if re.MatchString(text) {
			a.Hard = append(a.Hard, "命中自定义高风险规则 "+re.String())
		}
	}
	if cat == Install && installSource.re.MatchString(cmd) {
		a.Hard = append(a.Hard, installSource.name)
	}
	if cmd != "" {
		for _, part := range splitter.Split(cmd, -1) {
			if b := pushedProtected(strings.TrimSpace(part)); b != "" {
				a.Hard = append(a.Hard, "push 到受保护的分支 "+b)
			}
		}
	}
	for _, s := range softSignals {
		if s.re.MatchString(text) && !(s.name == "读写 .env 文件" && envFileExample.MatchString(text)) {
			a.Soft = append(a.Soft, s.name)
		}
	}
	if len(a.Hard) == 0 && !p.noRoutine {
		a.Routine = routine(cmd, r.Workdir, cat)
	}
	return a
}

// pushedProtected returns the protected branch a git push names, or "".
func pushedProtected(part string) string {
	f := strings.Fields(part)
	if len(f) < 2 || filepath.Base(f[0]) != "git" {
		return ""
	}
	i := 1
	for i < len(f) && f[i] != "push" {
		if f[i] == "-c" || f[i] == "-C" {
			i++
		}
		i++
	}
	if i >= len(f) {
		return ""
	}
	var pos []string
	for _, a := range f[i+1:] {
		if a == "--all" || a == "--tags" {
			return "（--all / --tags 会推送全部）"
		}
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
		}
	}
	// The first positional is the remote, the rest are refspecs.
	for k, a := range pos {
		if k > 0 && isProtectedBranch(a) {
			return a
		}
	}
	return ""
}

var (
	simpleCmd  = regexp.MustCompile("[;&|<>`\n]|\\$\\(")
	remoteName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	branchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]*$`)
	rmFlags    = regexp.MustCompile(`^(-[rRfv]+|--recursive|--force)$`)
	ghPRSafe   = regexp.MustCompile(`^gh\s+pr\s+(create|view|edit|ready|comment|checks|diff|list|status)\b`)
)

// artifactDirs are build output and caches an agent may delete freely.
// The first set is unambiguous anywhere in a path; the second only at the
// top of the working directory, where a source folder is unlikely to have
// such a name.
var (
	cacheDirs = map[string]bool{
		"node_modules": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true, ".cache": true,
		".parcel-cache": true, "__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true, ".gradle": true,
	}
	outputDirs = map[string]bool{"dist": true, "build": true, "out": true, "target": true, "coverage": true, "tmp": true}
)

// routine names a well-known, bounded action, or returns "". It only
// looks at one simple command, with no pipes, redirects or substitution.
func routine(cmd, workdir string, cat Category) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || simpleCmd.MatchString(cmd) {
		return ""
	}
	f := strings.Fields(cmd)
	switch cat {
	case GitRemote:
		if ghPRSafe.MatchString(cmd) {
			return "创建或查看 PR"
		}
		// git push [-u] <remote> <branch>
		if len(f) < 4 || f[0] != "git" || f[1] != "push" {
			return ""
		}
		args := f[2:]
		for len(args) > 0 && (args[0] == "-u" || args[0] == "--set-upstream") {
			args = args[1:]
		}
		if len(args) == 2 && remoteName.MatchString(args[0]) && branchName.MatchString(args[1]) && !isProtectedBranch(args[1]) {
			return "把分支 " + args[1] + " push 到 " + args[0]
		}
	case Delete:
		if f[0] != "rm" || len(f) < 2 {
			return ""
		}
		targets := 0
		for _, a := range f[1:] {
			if strings.HasPrefix(a, "-") {
				if !rmFlags.MatchString(a) {
					return ""
				}
				continue
			}
			if !isArtifactPath(a, workdir) {
				return ""
			}
			targets++
		}
		if targets > 0 {
			return "删除构建产物或缓存"
		}
	}
	return ""
}

// isArtifactPath reports whether a relative path is, or lies under, a
// build output or cache directory of the working directory.
func isArtifactPath(p, workdir string) bool {
	if p == "" || filepath.IsAbs(p) || strings.ContainsAny(p, "*?[{$~`") {
		return false
	}
	if outside([]string{p}, workdir) != "" || workdir == "" {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return false
	}
	parts := strings.Split(clean, "/")
	if outputDirs[parts[0]] {
		return true
	}
	for _, part := range parts {
		if cacheDirs[part] {
			return true
		}
	}
	return false
}

// Judge is DecideFor refined by an assessment: it returns the action and,
// when the assessment changed the table's answer, why.
//
//   - A hard signal sends the request to the user, in every mode but open
//     (which allows everything except the floor), even an operation the
//     table would allow, and never to the approver agent.
//   - In the auto mode a routine action is allowed without the approver.
func Judge(role, mode string, c Category, a Assessment) (Action, string) {
	act := DecideFor(role, mode, c)
	if Restricted(mode) {
		if act == Deny {
			return Deny, restrictedWhy[mode]
		}
		if len(a.Hard) > 0 {
			return Deny, a.Note() // a read of credentials, say: refused, not asked
		}
		return act, ""
	}
	if EffectiveMode(role, mode) == ModeOpen || act == Deny {
		return act, ""
	}
	if len(a.Hard) > 0 && (act == Allow || act == AskAI) {
		return AskUser, a.Note()
	}
	if act == AskAI && a.Routine != "" {
		return Allow, "常规操作，不需要审批 agent：" + a.Routine
	}
	return act, ""
}

var restrictedWhy = map[string]string{
	ModeReadOnly: "这是只读的会话，只能读文件和查看，不能改动或执行命令",
	ModeVerify:   "这是验收会话，只能读文件和运行测试、构建这类安全命令，不能改动",
}

// String describes a signal list for logs.
func (a Assessment) String() string {
	return fmt.Sprintf("hard=%v soft=%v routine=%q", a.Hard, a.Soft, a.Routine)
}

package policy

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const PolicyVersion = "approval-v1"

type CommandContext struct{ Workdir, CWD, Shell string }
type ScriptCandidate struct {
	Path, CWD, Inline, Language string
	UnsetEnv                    []string
}
type CommandView struct {
	Argv         []string
	CWDs         []string
	Start, End   uint
	Category     Category
	SignalText   string
	Assignments  []string
	Redirections []string
}
type CommandAnalysis struct {
	Status, Failure                 string
	Commands                        []CommandView
	Scripts                         []ScriptCandidate
	Effects                         []Category
	Category                        Category
	ReadOnly, Opaque, WritesOutside bool
}
type shellAnalyzer struct {
	result CommandAnalysis
	root   string
	count  int
	depth  int
	failed string
}
type flow struct{ after, success []string }

// ShellAnalysis reports whether commands are parsed as POSIX shell here.
// On Windows an agent's command may run in PowerShell or cmd, so it is not
// parsed: every command takes the strict fallback (exec-other, opaque), and
// approval goes to the user unless a rule allows it.
func ShellAnalysis() bool { return posixShell }

var posixShell = runtime.GOOS != "windows"

func AnalyzeCommand(cmd string, ctx CommandContext) CommandAnalysis {
	a := &shellAnalyzer{root: ctx.Workdir, result: CommandAnalysis{Status: "ok", Category: Read, ReadOnly: true}}
	if !ShellAnalysis() || (ctx.Shell != "" && ctx.Shell != "sh" && ctx.Shell != "bash" && ctx.Shell != "/bin/sh" && ctx.Shell != "/bin/bash") {
		a.failed = "unsupported shell"
	} else {
		a.parse(cmd, []string{first(ctx.CWD, ctx.Workdir)})
	}
	if a.failed != "" {
		return CommandAnalysis{Status: "fallback", Failure: a.failed, Category: ExecOther, Opaque: true}
	}
	a.result.ReadOnly = a.result.Category == Read && !a.result.Opaque
	return a.result
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func (a *shellAnalyzer) parse(src string, cwds []string) {
	if len(src) > 256<<10 || a.depth >= 16 {
		a.failed = "shell budget"
		return
	}
	a.depth++
	defer func() { a.depth-- }()
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		a.failed = "shell parse: " + err.Error()
		return
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		if n != nil {
			a.count++
		}
		return a.count <= 4096
	})
	if a.count > 4096 {
		a.failed = "AST budget"
		return
	}
	a.stmts(f.Stmts, cwds)
}
func (a *shellAnalyzer) add(c Category) {
	if rank[c] > rank[a.result.Category] {
		a.result.Category = c
	}
	for _, x := range a.result.Effects {
		if x == c {
			return
		}
	}
	a.result.Effects = append(a.result.Effects, c)
}
func mergeCWD(x, y []string) []string {
	r := append([]string{}, x...)
	for _, s := range y {
		found := false
		for _, v := range r {
			found = found || s == v
		}
		if !found {
			r = append(r, s)
		}
	}
	if len(r) > 16 {
		return []string{""}
	}
	return r
}
func (a *shellAnalyzer) stmts(ss []*syntax.Stmt, cwd []string) flow {
	f := flow{cwd, cwd}
	for _, s := range ss {
		f = a.stmt(s, f.after)
	}
	return f
}
func (a *shellAnalyzer) stmt(s *syntax.Stmt, cwd []string) flow {
	if a.failed != "" {
		return flow{cwd, cwd}
	}
	// Redirections apply to compound statements too.
	redirects := []string{}
	for _, r := range s.Redirs {
		if r.Hdoc != nil {
			a.expansions(r.Hdoc, cwd)
			continue
		}
		v, ok := literal(r.Word)
		a.expansions(r.Word, cwd)
		switch r.Op {
		case syntax.DplIn, syntax.DplOut: // descriptor duplication only
			if !ok || (v != "-" && strings.Trim(v, "0123456789") != "") {
				a.failed = "non-fd duplication"
			}
		case syntax.RdrIn:
			if ok {
				redirects = append(redirects, "<"+v)
			} else {
				a.opaque()
			}
		case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
			if !ok {
				a.opaque()
				a.add(OutsideWorktree)
				continue
			}
			redirects = append(redirects, ">"+v)
			if v == "/dev/null" || v == "/dev/stdout" || v == "/dev/stderr" {
				continue
			}
			a.add(EditWorktree)
			for _, d := range cwd {
				if d == "" || outside([]string{resolve(v, d)}, a.root) != "" {
					a.add(OutsideWorktree)
					a.result.WritesOutside = true
				}
			}
		default:
			a.failed = "unsupported redirection"
		}
	}
	out := flow{cwd, cwd}
	switch c := s.Cmd.(type) {
	case nil:
	case *syntax.CallExpr:
		out = a.call(c, s, cwd, redirects)
	case *syntax.BinaryCmd:
		x := a.stmt(c.X, cwd)
		switch c.Op {
		case syntax.AndStmt:
			y := a.stmt(c.Y, x.success)
			out = flow{mergeCWD(x.after, y.after), y.success}
		case syntax.OrStmt:
			y := a.stmt(c.Y, x.after)
			out = flow{mergeCWD(x.after, y.after), mergeCWD(x.success, y.success)}
		case syntax.Pipe, syntax.PipeAll:
			a.stmt(c.Y, cwd)
			out = flow{cwd, cwd}
			if shellConsumer(c.Y) {
				a.opaque()
				a.result.Commands = append(a.result.Commands, CommandView{SignalText: "eval pipe to interpreter", Category: ExecOther, CWDs: cwd})
			}
		default:
			a.failed = "unsupported binary command"
		}
	case *syntax.Subshell:
		a.stmts(c.Stmts, cwd)
	case *syntax.Block:
		out = a.stmts(c.Stmts, cwd)
	case *syntax.DeclClause:
		if c.Variant.Value != "export" {
			a.failed = "unsupported declaration"
			break
		}
		for _, v := range c.Args {
			if v.Name != nil && v.Name.Value == "CDPATH" {
				a.failed = "unknown CDPATH semantics"
			}
			if v.Value != nil {
				a.expansions(v.Value, cwd)
			}
		}
		a.result.Commands = append(a.result.Commands, CommandView{Argv: []string{"export"}, CWDs: cwd, SignalText: nodeText(c), Category: Read})
	default:
		a.failed = fmt.Sprintf("unsupported %T", c)
	}
	if s.Background || s.Negated {
		out = flow{mergeCWD(cwd, out.after), mergeCWD(cwd, out.success)}
	}
	return out
}
func nodeText(n syntax.Node) string {
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, n)
	return b.String()
}
func shellConsumer(s *syntax.Stmt) bool {
	c, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || len(c.Args) == 0 {
		return false
	}
	v, _ := literal(c.Args[0])
	return shellName(filepath.Base(v)) || scriptLanguage(filepath.Base(v)) != ""
}
func (a *shellAnalyzer) opaque() { a.result.Opaque = true; a.add(ExecOther) }
func (a *shellAnalyzer) expansions(w *syntax.Word, cwd []string) {
	if w == nil {
		return
	}
	syntax.Walk(w, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.CmdSubst:
			a.stmts(x.Stmts, cwd)
			return false
		case *syntax.ProcSubst:
			a.stmts(x.Stmts, cwd)
			a.opaque()
			return false
		case *syntax.ArithmExp:
			// Arithmetic assignments/array indices may execute more shell. Do not infer safety.
			text := nodeText(x)
			if strings.ContainsAny(strings.TrimPrefix(text, "$(("), "=$`[") {
				a.opaque()
			}
		}
		return true
	})
}
func literal(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	return literalParts(w.Parts, false)
}
func literalParts(parts []syntax.WordPart, quoted bool) (string, bool) {
	var b strings.Builder
	for _, p := range parts {
		switch v := p.(type) {
		case *syntax.Lit:
			text := v.Value
			// Decode backslash quoting without evaluating expansions.
			for i := 0; i < len(text); i++ {
				if text[i] == '\\' && i+1 < len(text) {
					n := text[i+1]
					if !quoted || strings.ContainsRune("$`\"\\\n", rune(n)) {
						i++
						if n != '\n' {
							b.WriteByte(n)
						}
						continue
					}
				}
				b.WriteByte(text[i])
			}
		case *syntax.SglQuoted:
			if v.Dollar {
				return "", false
			}
			b.WriteString(v.Value)
		case *syntax.DblQuoted:
			if v.Dollar {
				return "", false
			}
			s, ok := literalParts(v.Parts, true)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		case *syntax.ArithmExp:
			if strings.ContainsAny(strings.TrimPrefix(nodeText(v), "$(("), "=$`[") {
				return "", false
			}
			b.WriteString("0")
		default:
			return "", false
		}
	}
	return b.String(), true
}
func resolve(p, cwd string) string {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return p
	}
	return filepath.Join(cwd, p)
}
func shellName(s string) bool {
	return s == "sh" || s == "bash" || s == "dash" || s == "zsh" || s == "ksh"
}
func scriptLanguage(s string) string {
	switch s {
	case "python", "python3", "node", "ruby", "perl", "php", "deno", "bun", "tsx", "ts-node":
		return s
	}
	return ""
}
func unwrapAST(f []string) ([]string, bool) {
	for len(f) > 0 && isWrapper(f[0]) {
		name := filepath.Base(f[0])
		if name == "command" && len(f) == 3 && (f[1] == "-v" || f[1] == "-V") {
			return f, true
		}
		switch name {
		case "env":
			i := 1
			for i < len(f) {
				v := f[i]
				if v == "-u" || v == "--unset" {
					if i+1 >= len(f) {
						return nil, false
					}
					i += 2
				} else if strings.HasPrefix(v, "--unset=") || v == "--" || strings.Contains(v, "=") && !strings.HasPrefix(v, "-") {
					i++
				} else if strings.HasPrefix(v, "-") {
					return nil, false
				} else {
					break
				}
			}
			f = f[i:]
		case "timeout":
			i := 1
			for i < len(f) && strings.HasPrefix(f[i], "-") {
				if f[i] == "--preserve-status" || f[i] == "--foreground" {
					i++
				} else if f[i] == "-k" || f[i] == "-s" || f[i] == "--kill-after" || f[i] == "--signal" {
					i += 2
				} else {
					return nil, false
				}
			}
			if i >= len(f) {
				return nil, false
			}
			f = f[i+1:]
		case "xargs":
			i := 1
			for i < len(f) && f[i] == "-0" {
				i++
			}
			if i >= len(f) {
				return nil, false
			}
			f = f[i:]
		case "command", "exec", "nohup":
			if len(f) > 1 && strings.HasPrefix(f[1], "-") {
				return nil, false
			}
			f = f[1:]
		default:
			return nil, false
		}
	}
	return f, true
}
func (a *shellAnalyzer) call(c *syntax.CallExpr, s *syntax.Stmt, cwd []string, redirs []string) flow {
	view := CommandView{CWDs: append([]string{}, cwd...), Start: s.Pos().Offset(), End: s.End().Offset(), Redirections: redirs}
	for _, v := range c.Assigns {
		if v.Name != nil && v.Name.Value == "CDPATH" {
			a.failed = "unknown CDPATH semantics"
		}
		if v.Value != nil {
			a.expansions(v.Value, cwd)
		}
		view.Assignments = append(view.Assignments, nodeText(v))
	}
	known := true
	for _, w := range c.Args {
		v, ok := literal(w)
		a.expansions(w, cwd)
		known = known && ok
		if !ok {
			v = nodeText(w)
		}
		view.Argv = append(view.Argv, v)
	}
	unset := explicitUnset(view.Argv)
	for _, assignment := range view.Assignments {
		if strings.HasPrefix(assignment, "BASH_ENV=") || strings.HasPrefix(assignment, "ENV=") {
			unset = nil
		}
	}
	f, unwrapped := unwrapAST(view.Argv)
	if !unwrapped {
		a.opaque()
		view.Category = ExecOther
		view.SignalText = nodeText(c)
		a.result.Commands = append(a.result.Commands, view)
		return flow{cwd, cwd}
	}
	if len(f) == 0 {
		view.SignalText = strings.Join(view.Assignments, " ")
		a.result.Commands = append(a.result.Commands, view)
		return flow{cwd, cwd}
	}
	name := filepath.Base(f[0])
	if name == "zsh" || name == "ksh" {
		a.failed = "unsupported nested shell"
		return flow{cwd, cwd}
	}
	view.Argv = f
	if _, ok := literal(c.Args[0]); !ok {
		a.opaque()
	}
	safe := append([]string{}, f...)
	signal := append([]string{}, f...)
	signal[0] = name
	if name == "echo" || name == "printf" {
		for i := 1; i < len(f); i++ {
			safe[i] = "DATA"
			signal[i] = "DATA"
		}
	}
	if name == "git" && len(f) > 1 && f[1] == "commit" {
		for i := 2; i < len(f); i++ {
			if (f[i] == "-m" || f[i] == "--message") && i+1 < len(f) {
				i++
				safe[i] = "DATA"
				signal[i] = "DATA"
			} else if strings.HasPrefix(f[i], "--message=") {
				safe[i] = "--message=DATA"
				signal[i] = safe[i]
			}
		}
	}
	view.SignalText = strings.Join(append(signal, view.Assignments...), " ") + " " + strings.Join(redirs, " ")
	cat := Read
	// Path checks use current cwd, always against the original trust root.
	for _, d := range cwd {
		fc := append([]string{}, safe...)
		for i := 1; i < len(fc); i++ {
			v := fc[i]
			if !strings.HasPrefix(v, "-") && (strings.HasPrefix(v, "../") || strings.HasPrefix(v, "./")) {
				fc[i] = resolve(v, d)
			}
		}
		x, _ := classifyArgv(fc, strings.Join(fc, " "), a.root)
		if (d == "" && a.root != "") || d != "" && outside([]string{d}, a.root) != "" && x != Read && rank[x] < rank[OutsideWorktree] {
			x = OutsideWorktree
		}
		if rank[x] > rank[cat] {
			cat = x
		}
	}
	if readOnlyInvocation(f) {
		cat = Read
	} else if name == "systemctl" {
		cat = Service
	}
	// Preserve the operation independently of outside path classification.
	baseEffect, _ := classifyArgv(safe, strings.Join(safe, " "), "")
	if readOnlyInvocation(f) {
		baseEffect = Read
	}
	if baseEffect == EditWorktree || baseEffect == Delete || baseEffect == Service || baseEffect == Irreversible {
		a.add(baseEffect)
	}
	if name == "command" && len(f) == 3 && (f[1] == "-v" || f[1] == "-V") {
		cat = Read
	}
	// Preserve fundamental effects even when a path category obscures them.
	switch name {
	case "rm", "rmdir", "unlink", "shred":
		a.add(Delete)
	case "cp", "mv", "mkdir", "touch", "tee":
		a.result.Effects = append(a.result.Effects, EditWorktree)
	}
	if cat == EditWorktree || cat == Delete || cat == Service || cat == Irreversible {
		a.result.Effects = append(a.result.Effects, cat)
	}
	if name == "cd" {
		targets := []string{}
		if len(f) != 2 || !known || f[1] == "-" || strings.ContainsAny(f[1], "$*?[~") {
			targets = []string{""}
			cat = OutsideWorktree
		} else {
			for _, d := range cwd {
				if d == "" {
					targets = append(targets, "")
				} else {
					targets = append(targets, resolve(f[1], d))
				}
			}
		}
		view.Category = cat
		a.add(cat)
		a.result.Commands = append(a.result.Commands, view)
		return flow{mergeCWD(cwd, targets), targets}
	}
	if name == "find" {
		for i, v := range f {
			if oneOf(v, "-exec", "-execdir", "-ok", "-okdir") {
				inner := []string{}
				for _, arg := range f[i+1:] {
					if arg == ";" || arg == "+" {
						break
					}
					inner = append(inner, arg)
				}
				nestedCWD := cwd
				if v == "-execdir" || v == "-okdir" {
					nestedCWD = []string{""}
				}
				a.argv(inner, nestedCWD)
			}
		}
	}
	if name == "eval" {
		if known && len(f) > 1 {
			a.parse(strings.Join(f[1:], " "), cwd)
		} else {
			a.opaque()
		}
	}
	if name == "source" || name == "." {
		a.opaque()
		if known && len(f) == 2 {
			for _, d := range cwd {
				a.result.Scripts = append(a.result.Scripts, ScriptCandidate{Path: f[1], CWD: d, Language: "sh"})
			}
		}
	}
	if lang := scriptLanguage(name); lang != "" {
		for i, arg := range f[1:] {
			if oneOf(arg, "-c", "-e", "--eval") && i+2 < len(f) {
				body := f[i+2]
				a.result.Scripts = append(a.result.Scripts, ScriptCandidate{Inline: body, Language: lang})
				a.opaque()
				view.SignalText += "\n" + body
				break
			}
		}
	}
	if shellName(name) {
		inline := ""
		found := false
		for i := 1; i < len(f); i++ {
			if strings.HasPrefix(f[i], "-") && strings.Contains(f[i], "c") {
				if known && i+1 < len(f) {
					inline = f[i+1]
					found = true
				}
				break
			}
		}
		if found {
			cat = Read
			a.parse(inline, cwd)
			a.result.Scripts = append(a.result.Scripts, ScriptCandidate{Inline: inline, Language: name})
		} else {
			for _, r := range s.Redirs {
				if r.Hdoc != nil {
					inline = nodeText(r.Hdoc)
					cat = Read
					a.parse(inline, cwd)
					found = true
					a.result.Scripts = append(a.result.Scripts, ScriptCandidate{Inline: inline, Language: name})
				}
			}
			if !found {
				a.opaque()
			}
		}
	}
	if lang := scriptLanguage(name); lang != "" {
		for _, r := range s.Redirs {
			if r.Hdoc != nil {
				body := nodeText(r.Hdoc)
				a.result.Scripts = append(a.result.Scripts, ScriptCandidate{Inline: body, Language: lang})
				if name == "python3" || name == "python" {
					if paths, ok := staticPythonWrites(body); ok {
						for _, p := range paths {
							view.SignalText += "\n" + p
							a.result.Effects = append(a.result.Effects, EditWorktree)
							for _, d := range cwd {
								if d == "" || outside([]string{resolve(p, d)}, a.root) != "" {
									cat = OutsideWorktree
								}
							}
						}
					} else {
						view.SignalText += "\n" + body
						a.opaque()
					}
				} else {
					view.SignalText += "\n" + body
					a.opaque()
				}
			}
		}
	}
	if path := scriptPath(f); path != "" {
		for _, d := range cwd {
			a.result.Scripts = append(a.result.Scripts, ScriptCandidate{Path: path, CWD: d, Language: name, UnsetEnv: unset})
		}
		a.result.Opaque = true
	}
	if !known && name != "echo" && name != "printf" && cat == Read {
		a.opaque()
		cat = ExecOther
	}
	if cat == OutsideWorktree {
		a.result.WritesOutside = cat != Read
	}
	view.Category = cat
	a.add(cat)
	a.result.Commands = append(a.result.Commands, view)
	return flow{cwd, cwd}
}
func scriptPath(f []string) string {
	if len(f) == 0 {
		return ""
	}
	name := filepath.Base(f[0])
	if shellName(name) || scriptLanguage(name) != "" {
		for _, v := range f[1:] {
			if v == "-" || v == "-m" || v == "-c" || (!shellName(name) && v == "-e") || v == "--eval" || strings.ContainsAny(v, "$`*") {
				return ""
			}
			if strings.HasPrefix(v, "-") {
				if strings.ContainsAny(v, "cm") || !shellName(name) && strings.Contains(v, "e") {
					return ""
				}
				continue
			}
			return v
		}
		return ""
	}
	if strings.HasPrefix(f[0], "./") || strings.HasSuffix(f[0], ".sh") {
		return f[0]
	}
	return ""
}
func classifyCommand(cmd, workdir string) (Category, string) {
	return classifyRequestCommand(cmd, Request{Workdir: workdir})
}
func classifyRequestCommand(cmd string, r Request) (Category, string) {
	x := AnalyzeCommand(cmd, CommandContext{r.Workdir, r.CWD, r.Shell})
	if x.Status != "ok" {
		c, w := classifyCommandLegacy(cmd, r.Workdir)
		if rank[c] < rank[ExecOther] {
			c = ExecOther
		}
		return c, "解析回退：" + x.Failure + " " + w
	}
	return x.Category, ""
}

func (a *shellAnalyzer) argv(f []string, cwd []string) {
	if len(f) == 0 {
		return
	}
	if a.depth >= 16 {
		a.failed = "execution recursion budget"
		return
	}
	a.depth++
	defer func() { a.depth-- }()
	call := &syntax.CallExpr{}
	for _, v := range f {
		call.Args = append(call.Args, &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: v}}})
	}
	stmt := &syntax.Stmt{Cmd: call}
	a.call(call, stmt, cwd, nil)
}

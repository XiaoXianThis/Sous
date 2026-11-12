package policy

import (
	"strings"
	"testing"
)

func TestShellBudgetsUnsupportedAndRawRules(t *testing.T) {
	needShell(t)
	p, _ := New(Config{ExtraHard: []string{`dangerous value`}})
	for _, cmd := range []string{strings.Repeat("true;", 2000), strings.Repeat("x", (256<<10)+1), `zsh -c 'echo safe'`, `ksh -c 'echo safe'`, strings.Repeat("nohup ", 5000) + "true"} {
		r := exec(cmd)
		c, _ := p.Classify(r)
		a := p.Assess(r, c)
		act, _ := Judge(RoleDev, ModeReadOnly, c, a)
		if act != Deny {
			t.Fatalf("fallback granted readonly: %s %+v", c, a)
		}
	}
	for _, shell := range []string{"powershell", "cmd", "fish", "zsh"} {
		r := exec("echo safe")
		r.Shell = shell
		c, _ := p.Classify(r)
		a := p.Assess(r, c)
		if a.ParseStatus != "fallback" || c == Read || c == ExecSafe {
			t.Fatalf("%s: %s %+v", shell, c, a)
		}
	}
	for _, cmd := range []string{"CDPATH=/outside cd apps && touch file", "export CDPATH=/outside; cd apps && touch file"} {
		x := AnalyzeCommand(cmd, CommandContext{Workdir: "/work"})
		if x.Status != "fallback" {
			t.Fatal("CDPATH guessed cwd", cmd)
		}
	}
	r := Request{ToolKind: "execute", Workdir: "/work", RawInput: []byte(`{"command":["echo","dangerous","value"]}`)}
	c, _ := p.Classify(r)
	if len(p.Assess(r, c).Hard) == 0 {
		t.Fatal("custom raw rule lost argv boundary compatibility")
	}
	for _, cmd := range []string{"npm publish /outside", "sudo cat /outside", "git push --force /outside", "git -C /outside push --force origin task", "git --no-pager -c core.pager=cat push --force origin task"} {
		c, _ := p.Classify(exec(cmd))
		if c != Irreversible {
			t.Fatalf("outside masked irreversible: %s => %s", cmd, c)
		}
	}
	for _, kind := range []string{"edit", "delete", "move"} {
		r := exec("cat /outside")
		r.ToolKind = kind
		r.Paths = []string{"/outside"}
		c, _ := p.Classify(r)
		act, _ := Judge(RoleServer, ModeAuto, c, p.Assess(r, c))
		if act != AskUser {
			t.Fatalf("declared effect hidden: %s => %s", kind, act)
		}
	}
}

func FuzzShellConservativeFallback(f *testing.F) {
	for _, seed := range []string{"echo hi", "echo 'unclosed", "bash -c 'echo x'", "cat <<'EOF'\ndata\nEOF", "rm -rf /", "cd x && cat > ../f"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		if len(cmd) > 300<<10 {
			t.Skip()
		}
		p, _ := New(Config{})
		r := exec(cmd)
		c, _ := p.Classify(r)
		a := p.Assess(r, c)
		if a.ParseStatus == "fallback" {
			act, _ := Judge(RoleDev, ModeReadOnly, c, a)
			if act == Allow {
				t.Fatal("fallback newly allowed")
			}
		}
		if p.floorHit(cmd) != "" && c != Floor {
			t.Fatal("floor lost")
		}
	})
}

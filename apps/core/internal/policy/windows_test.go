package policy

import (
	"os"
	"testing"
)

// SOUS_TEST_NO_SHELL=1 runs this package as on Windows (no POSIX parsing),
// so the Windows behaviour can be checked on any machine.
func init() {
	if os.Getenv("SOUS_TEST_NO_SHELL") == "1" {
		posixShell = false
	}
}

// needShell skips a test that checks POSIX shell parsing where commands are
// not parsed (Windows).
func needShell(t *testing.T) {
	t.Helper()
	if !ShellAnalysis() {
		t.Skip("commands are not parsed as POSIX shell here (Windows)")
	}
}

// Without parsing, every command takes the strict fallback: nothing is read
// only, and custom and built-in high-risk rules still fire.
func TestNoShellAnalysisStaysStrict(t *testing.T) {
	saved := posixShell
	posixShell = false
	defer func() { posixShell = saved }()
	p, _ := New(Config{ExtraHard: []string{`dangerous value`}})
	for _, cmd := range []string{"cat README.md", "ls", "git status"} {
		r := exec(cmd)
		c, _ := p.Classify(r)
		a := p.Assess(r, c)
		if a.ParseStatus != "fallback" || c == Read || c == ExecSafe {
			t.Fatalf("%s: %s %+v", cmd, c, a)
		}
		if act, _ := Judge(RoleDev, ModeReadOnly, c, a); act != Deny {
			t.Fatalf("%s granted in a read-only session", cmd)
		}
	}
	r := Request{ToolKind: "execute", Workdir: "/work", RawInput: []byte(`{"command":["echo","dangerous","value"]}`)}
	c, _ := p.Classify(r)
	if len(p.Assess(r, c).Hard) == 0 {
		t.Fatal("custom rule missed an argv command")
	}
	r = exec("cat ~/.ssh/id_ed25519")
	c, _ = p.Classify(r)
	if len(p.Assess(r, c).Hard) == 0 {
		t.Fatal("credential read lost its high-risk signal")
	}
}

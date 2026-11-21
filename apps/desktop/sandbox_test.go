package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSandboxRequestBoundary(t *testing.T) {
	for _, c := range []struct {
		origin, message string
		main, want      bool
	}{
		{sandboxURL, `sous.sandbox:{"id":1,"action":"pick-source"}`, true, true},
		{sandboxURL, `sous.sandbox:{"id":2,"action":"run","value":"printf test","offline":true}`, true, true},
		{"https://agent.wxian.cc", `sous.sandbox:{"id":1,"action":"apply"}`, true, false},
		{"http://localhost:8787/", `sous.sandbox:{"id":1,"action":"run"}`, true, false},
		{"wails://localhost/", `sous.sandbox:{"id":1,"action":"run"}`, true, false},
		{sandboxURL, `sous.sandbox:{"id":1,"action":"run"}`, false, false},
		{sandboxURL, `sous.sandbox:{"id":1,"action":"run","source":"/tmp/other"}`, true, false},
		{sandboxURL, `sous.sandbox:{"id":1} {}`, true, false},
		{sandboxURL, `sous.sandbox:{"id":-1}`, true, false},
		{sandboxURL, "sous.sandbox:{}" + strings.Repeat(" ", 16384), true, false},
	} {
		got, ok := parseSandboxRequest(c.message, c.main, c.origin)
		if ok != c.want {
			t.Fatalf("bad request boundary: %q from %q: %v", c.message[:min(128, len(c.message))], c.origin, ok)
		}
		if ok && (got.ID == 0 || got.Action == "") {
			t.Fatal("native request lost decoded fields")
		}
	}
}

func TestNativeSandboxProgressRemovesControlsAndBoundsOutput(t *testing.T) {
	var seen []string
	p := sandboxProgress{update: func(s string) { seen = append(seen, s) }}
	_, _ = p.Write([]byte("pre"))
	_, _ = p.Write([]byte("pare\nuser\x1b\u202e\n"))
	if strings.Join(seen, "/") != "prepare/user��" {
		t.Fatalf("unexpected progress: %q", seen)
	}
	buf := sandboxOutput{limit: 3}
	if n, err := buf.Write([]byte("abcde")); n != 5 || err != nil || !buf.overflow || buf.String() != "abc" {
		t.Fatal("response bound failed")
	}
}

func TestSandboxRuntimeHomeDoesNotDependOnProfileLength(t *testing.T) {
	s := &sandboxHost{home: "/Users/example/" + strings.Repeat("project/", 80)}
	got := s.runtimeHome()
	if len(got) > 48 || !strings.HasPrefix(got, "/private/tmp/sous-vm-") || got != s.runtimeHome() {
		t.Fatalf("invalid short runtime home %q", got)
	}
	if got == (&sandboxHost{home: "/Users/example/other"}).runtimeHome() {
		t.Fatal("two profiles share a runtime")
	}
}

func TestLocalRuntimePreferencesDoNotPersistProjectGrants(t *testing.T) {
	s := &sandboxHost{home: t.TempDir(), msb: "/runtime/msb", firmware: "/runtime/fw", image: "/runtime/alpine.tar", grant: sandboxGrant{path: "/private/project-secret", identity: "grant-secret"}}
	for _, name := range []string{"SOUS_SANDBOX_MSB", "SOUS_SANDBOX_FIRMWARE", "SOUS_SANDBOX_IMAGE_ARCHIVE"} {
		t.Setenv(name, "")
	}
	if err := s.saveSetup(); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(s.home, "sandbox-runtime.json"))
	if err != nil || strings.Contains(string(stored), "secret") {
		t.Fatalf("saved private project grant: %v", err)
	}
	next := &sandboxHost{home: s.home}
	next.loadSetup()
	if next.msb != s.msb || next.firmware != s.firmware || next.image != s.image || next.grant.path != "" {
		t.Fatal("setup was not restored independently from the grant")
	}
}

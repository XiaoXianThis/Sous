package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func scanInit(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remote}} {
		if remote == "" && args[0] == "remote" {
			continue
		}
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestScan(t *testing.T) {
	ws := t.TempDir()
	scanInit(t, filepath.Join(ws, "a"), "https://github.com/x/a.git")
	scanInit(t, filepath.Join(ws, "group", "b"), "")
	scanInit(t, filepath.Join(ws, "deep", "x", "c"), "") // three levels: not found
	scanInit(t, filepath.Join(ws, "node_modules", "d"), "")
	scanInit(t, filepath.Join(ws, ".hidden"), "")
	for _, d := range []string{"apps", "build", "docs", "work"} {
		os.MkdirAll(filepath.Join(ws, "a", d), 0o755)
	}
	os.WriteFile(filepath.Join(ws, "a", "AGENTS.md"), []byte("#"), 0o644)
	os.WriteFile(filepath.Join(ws, "group", "b", "f"), []byte("x"), 0o644)

	repos := Scan(context.Background(), []string{ws, filepath.Join(ws, "missing")})
	if len(repos) != 2 {
		t.Fatalf("repos = %+v", repos)
	}
	a, b := repos[0], repos[1]
	if a.Path != filepath.Join(ws, "a") || a.Remote != "https://github.com/x/a.git" || !a.Wokspace || !a.Dirty {
		t.Fatalf("a = %+v", a)
	}
	if b.Path != filepath.Join(ws, "group", "b") || b.Remote != "" || b.Wokspace || !b.Dirty {
		t.Fatalf("b = %+v", b)
	}
}

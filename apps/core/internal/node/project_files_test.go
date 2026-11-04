package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

func TestProjectFiles(t *testing.T) {
	ws := t.TempDir()
	root := filepath.Join(ws, "demo")
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("hi"), 0o644)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("x"), 0o644)
	os.Symlink(outside, filepath.Join(root, "link.txt"))

	d := &Daemon{cfg: Config{Workspaces: []string{ws}}}
	a := &admin{d: d}
	call := func(op, rel, content, sha string) (any, error) {
		return a.projectOp(op, proto.ProjectArgs{Root: root, Rel: rel, Content: content, BaseSHA256: sha})
	}

	dir, err := call(proto.OpProjectLs, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range dir.(proto.ProjectDir).Entries {
		if e.Name == ".git" {
			t.Fatal(".git listed")
		}
	}
	f, err := call(proto.OpProjectRead, "src/a.txt", "", "")
	if err != nil || f.(proto.ProjectFile).Content != "hi" {
		t.Fatalf("read: %v %v", f, err)
	}
	if _, err := call(proto.OpProjectWrite, "src/a.txt", "yo", "stale"); err == nil {
		t.Fatal("stale write accepted")
	}
	if _, err := call(proto.OpProjectWrite, "src/a.txt", "yo", f.(proto.ProjectFile).SHA256); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src", "a.txt")); string(b) != "yo" {
		t.Fatalf("written %q", b)
	}
	for _, rel := range []string{"../x", "link.txt", ".git/config"} {
		if _, err := call(proto.OpProjectRead, rel, "", ""); err == nil {
			t.Fatalf("%s readable", rel)
		}
	}
	if _, err := a.projectOp(proto.OpProjectLs, proto.ProjectArgs{Root: t.TempDir()}); err == nil {
		t.Fatal("root outside workspaces served")
	}
}

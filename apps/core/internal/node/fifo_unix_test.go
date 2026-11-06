//go:build !windows

package node

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// project_read refuses a FIFO instead of blocking on it (end-to-end review L2).
func TestProjectReadRefusesAFIFO(t *testing.T) {
	ws := t.TempDir()
	root := filepath.Join(ws, "demo")
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skip(err)
	}
	a := &admin{d: &Daemon{cfg: Config{Workspaces: []string{ws}}}}
	if _, err := a.projectOp(proto.OpProjectRead, proto.ProjectArgs{Root: root, Rel: "pipe"}); err == nil {
		t.Fatal("read a FIFO")
	}
}

package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

// A freshly created project must be able to host task worktrees. Before the
// initial commit, PrepareTask failed with "no commits yet" and every run died.
func TestInitialCommitMakesRepoUsable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := workspace.Run(ctx, dir, "git", "init", "-q"); err != nil {
		t.Fatal(err)
	}
	workspace.Run(ctx, dir, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	start := workspace.Start{Branch: "main", From: workspace.FromLocal}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := workspace.PrepareTaskOn(ctx, t.TempDir(), dir, "task-1", "", "", start); err == nil {
		t.Fatal("PrepareTask on a repository without commits should fail with a clear error")
	}

	if err := initialCommit(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := initialCommit(ctx, dir); err != nil {
		t.Fatalf("second initialCommit should be a no-op: %v", err)
	}
	ws, err := workspace.PrepareTaskOn(ctx, t.TempDir(), dir, "task-1", "", "", start)
	if err != nil {
		t.Fatalf("PrepareTask after initial commit: %v", err)
	}
	if ws.Base == "" {
		t.Fatal("worktree has no base commit")
	}
	if _, err := os.Stat(filepath.Join(ws.Dir, "AGENTS.md")); err != nil {
		t.Fatalf("worktree is missing the project's files: %v", err)
	}
}

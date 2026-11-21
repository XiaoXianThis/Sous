package brain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func dataFixture(t *testing.T, dir, marker string) {
	t.Helper()
	var s *store.Store
	var err error
	if marker != "" {
		s, err = store.OpenInitialized(filepath.Join(dir, "hub.db"), "workspace.local_identity", marker, false)
	} else {
		s, err = store.Open(filepath.Join(dir, "hub.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRejectsEitherWrongAssemblyBeforeReplacingLiveData(t *testing.T) {
	for _, entry := range []struct{ name, current, staged string }{{"wrong destination", "local-v1", ""}, {"wrong payload", "", "local-v1"}} {
		t.Run(entry.name, func(t *testing.T) {
			root := t.TempDir()
			current, staged := filepath.Join(root, "data"), filepath.Join(root, "staged")
			dataFixture(t, current, entry.current)
			dataFixture(t, staged, entry.staged)
			before, err := os.ReadFile(filepath.Join(current, "hub.db"))
			if err != nil {
				t.Fatal(err)
			}
			if err := installPairedSnapshot(context.Background(), current, staged); err == nil {
				t.Fatal("foreign assembly snapshot installed")
			}
			after, err := os.ReadFile(filepath.Join(current, "hub.db"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed preflight changed live data", err)
			}
			if backups, err := filepath.Glob(current + ".before-*"); err != nil || len(backups) != 0 {
				t.Fatal("preflight moved live workspace", backups, err)
			}
		})
	}
}

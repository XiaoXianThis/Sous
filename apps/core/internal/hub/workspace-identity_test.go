package hub

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestWorkspaceIdentityFencesAndKeepsStandaloneRestarts(t *testing.T) {
	ctx := context.Background()
	cfg := config.ForLocalWorkspace(t.TempDir())
	h, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Log.EnsureMainThread(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(DBPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	paired := *cfg
	paired.LocalWorkspace = false
	if other, err := Open(&paired, nil); err == nil {
		_ = other.Close()
		t.Fatal("paired assembly opened local state")
	}
	cloud := hostedFixture(t)
	cloud.DataDir = cfg.DataDir
	if other, err := Open(cloud, nil); err == nil {
		_ = other.Close()
		t.Fatal("hosted assembly adopted local state")
	}
	after, err := os.ReadFile(DBPath(cfg))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("an invalid assembly modified the database", err)
	}
	reopened, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if v, err := reopened.Store.Setting(ctx, localKindKey); err != nil || v != localKind {
		t.Fatal("wrong local identity", v, err)
	}
}

func TestKnownDesktopPreviewUpgradesOnlyAsLocal(t *testing.T) {
	ctx := context.Background()
	cfg := config.ForLocalWorkspace(t.TempDir())
	s, err := store.Open(filepath.Join(cfg.DataDir, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, bindingKey, `{"selection":"none","generation":5}`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckPairedDatabase(ctx, cfg.DataDir); err == nil {
		t.Fatal("old Desktop binding was interpreted as paired")
	}
	h, err := Open(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if v, err := h.Store.Setting(ctx, localKindKey); err != nil || v != localKind {
		t.Fatal("known local preview was not marked", v, err)
	}
	if v, err := h.Store.Setting(ctx, bindingKey); err != nil || v != `{"selection":"none","generation":5}` {
		t.Fatal("existing binding was overwritten", v, err)
	}
}

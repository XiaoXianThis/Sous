package hub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
)

const hostedOriginKey = "workspace.hosted_origin"

var errHostedOnWindows = errors.New("hosted-hub requires POSIX private directory permissions and is not supported on Windows")

func verifyHostedDirectory(cfg *config.Hub) error {
	if strings.ContainsAny(cfg.DataDir, "?#") {
		return errors.New("invalid hosted database directory")
	}
	dir, err := os.Lstat(cfg.DataDir)
	if err != nil {
		return err
	}
	if !dir.IsDir() || dir.Mode().Perm()&0o077 != 0 {
		return errors.New("hosted data_dir must be a private directory (0700), not a symlink")
	}
	data, err := filepath.EvalSymlinks(cfg.DataDir)
	if err != nil {
		return err
	}
	web, err := filepath.EvalSymlinks(cfg.WebDir)
	if err != nil {
		return err
	}
	if containsPath(data, web) || containsPath(web, data) {
		return errors.New("hosted WebUI and private data directories must be disjoint")
	}
	return nil
}

func containsPath(base, name string) bool {
	rel, err := filepath.Rel(base, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CreateHostedSetup is used only by the stopped-service operator command. It
// does not reset an initialized deployment or expose a token in service logs.
func (h *Hub) CreateHostedSetup(ctx context.Context) (string, error) {
	if !h.Cfg.HostedWorkspace {
		return "", errors.New("hosted setup requires a hosted workspace")
	}
	has, err := h.Auth.HasPasskey(ctx)
	if err != nil {
		return "", err
	}
	if has {
		return "", errors.New("a Passkey is already registered; refusing to issue another initial setup link")
	}
	token, err := h.Auth.NewSetupToken(ctx)
	if err != nil {
		return "", err
	}
	return h.Auth.SetupURL(token), nil
}

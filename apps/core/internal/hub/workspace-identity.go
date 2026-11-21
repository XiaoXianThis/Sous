package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const localKindKey = "workspace.local_identity"
const localKind = "local-v1"

type workspaceIdentity struct {
	key, value        string
	initialize, adopt bool
}

func intendedIdentity(cfg *config.Hub) workspaceIdentity {
	if cfg.HostedWorkspace {
		return workspaceIdentity{key: hostedOriginKey, value: cfg.PublicURL, initialize: true}
	}
	if cfg.LocalWorkspace {
		return workspaceIdentity{key: localKindKey, value: localKind, initialize: true}
	}
	return workspaceIdentity{}
}

// CheckPairedDatabase is the read-only assembly fence used before accepting
// a Relay snapshot. An absent destination is allowed; a direct workspace is not.
func CheckPairedDatabase(ctx context.Context, dataDir string) error {
	_, err := preflightWorkspace(ctx, &config.Hub{DataDir: dataDir})
	return err
}

func preflightWorkspace(ctx context.Context, cfg *config.Hub) (workspaceIdentity, error) {
	var none workspaceIdentity
	path := DBPath(cfg)
	if cfg.DirectWorkspace() && strings.ContainsAny(cfg.DataDir, "?#") {
		return none, errors.New("invalid standalone workspace data directory")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return intendedIdentity(cfg), nil
	}
	if err != nil {
		return none, err
	}
	if !info.Mode().IsRegular() {
		return none, errors.New("workspace database must be a regular file, not a link")
	}
	meta, empty, err := store.InspectWorkspace(ctx, path, hostedOriginKey, localKindKey, bindingKey)
	if err != nil {
		return none, fmt.Errorf("cannot verify workspace database assembly: %w", err)
	}
	if empty {
		return intendedIdentity(cfg), nil
	}
	hosted, hSet := meta[hostedOriginKey]
	local, lSet := meta[localKindKey]
	prior, priorSet := meta[bindingKey]
	// An earlier Desktop preview could persist the built-in binding before
	// the explicit local identity existed. This is positive local evidence,
	// unlike a path convention, a schema version or the absence of old nodes.
	var preview bindingState
	priorLocal := priorSet && json.Unmarshal([]byte(prior), &preview) == nil && preview.Generation > 0 &&
		(preview.Selection == "none" || preview.Selection == "legacy" || preview.Selection == "acp")
	switch {
	case cfg.HostedWorkspace && hSet && hosted == cfg.PublicURL && !lSet && !priorSet:
		return none, nil
	case cfg.LocalWorkspace && !hSet && lSet && local == localKind:
		return none, nil
	case cfg.LocalWorkspace && !hSet && !lSet && priorLocal:
		return workspaceIdentity{key: localKindKey, value: localKind, adopt: true}, nil
	case !cfg.DirectWorkspace() && !hSet && !lSet && !priorSet:
		return none, nil // the released paired format does not have a direct-workspace identity
	default:
		return none, errors.New("database belongs to another workspace assembly or HTTPS origin, or is unmarked; keep it intact and use its original assembly")
	}
}

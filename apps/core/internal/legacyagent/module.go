// Package legacyagent assembles the original in-process model loop explicitly
// for existing brain deployments. It is not a dependency of the Hub.
package legacyagent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/attach"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/httpapi"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/mcp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/models"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/supervisor"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// New preserves the existing provider, role, compaction and MCP behavior for
// assemblies that opt into compatibility mode.
func New(d hub.AgentDeps) (hub.AgentModule, error) {
	registry := models.New(d.Log)
	registry.Wrap = func(p provider.Provider) provider.Provider { return provider.WithRetry(p, 3, 2*time.Second) }
	if err := registry.Seed(context.Background(), d.Config); err != nil {
		return hub.AgentModule{}, fmt.Errorf("import models: %w", err)
	}
	available := func(ctx context.Context) bool { return registry.Configured(ctx, config.RoleSupervisor) }
	if !available(context.Background()) {
		slog.Warn("no model is configured for the supervisor yet: add a provider in Settings → Models")
	}
	role := func(r string) task.Model { return task.Model{Provider: registry.For(r), Name: r} }
	sup := supervisor.New(supervisor.Options{
		Log:      d.Log,
		Provider: registry.For(config.RoleSupervisor),
		Fixed:    registry.Fixed,
		Model:    config.RoleSupervisor,
		Tools:    d.Tools,
		Environment: func(ctx context.Context, threadID string) []string {
			return append(d.Nodes.Environment(ctx), d.Tasks.Environment(ctx, threadID)...)
		},
		DescribeContext: d.Tasks.DescribeContext,
		Attachments:     attach.Open(filepath.Join(d.Config.DataDir, "attachments")),
		Compactor:       registry.For(config.RoleCompactor),
		CompactorModel:  config.RoleCompactor,
		ContextWindow: func(ctx context.Context) int {
			return registry.ContextWindow(ctx, config.RoleSupervisor)
		},
		Gather:                  supervisor.DefaultGather,
		KeepPendingWithoutModel: d.Config.LocalWorkspace,
	})
	m := hub.AgentModule{
		Name: "legacy", Start: sup.Start, Wait: sup.Wait, Active: sup.Active,
		PauseIfIdle: sup.PauseIfIdle, Resume: sup.Resume, Available: available, RoleRefs: registry.RoleRefs,
		Routes:   []httpapi.Routes{registry.Routes(d.Auth.RequireFresh), sup.Routes},
		Reviewer: role(config.RoleReviewer), Approver: role(config.RoleApprover), Inspector: role(config.RoleInspector),
	}
	if !d.Config.MCP.Disabled {
		manager := mcp.NewManager(d.Log, d.Tools)
		manager.StdioDisabled = d.Config.MCP.StdioDisabled
		for _, t := range manager.Tools() {
			d.Tools.Register(t)
		}
		m.Routes = append(m.Routes, manager.Routes(d.Auth.RequireFresh))
		m.Start = func(ctx context.Context) {
			sup.Start(ctx)
			if err := manager.Start(ctx); err != nil {
				slog.Warn("mcp: cannot start the servers", "err", err)
			}
		}
		m.Wait = func() {
			sup.Wait()
			manager.Stop()
		}
	}
	return m, nil
}

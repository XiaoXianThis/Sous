package task

import (
	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// ApprovalPolicy is local Hub configuration. Observation gates are documented
// in the implementation plan; neither a model nor a node can enable them.
type ApprovalPolicy struct {
	AllowMedium       bool `yaml:"allow_medium"`
	MaxAIAllowsPerRun int  `yaml:"max_ai_allows_per_run"`
}

func eligibleMedium(f *proto.PolicyFacts, category string) bool {
	if f == nil || f.Version != 1 || f.PolicyVersion != policy.PolicyVersion || f.ParseStatus != "ok" || !f.EvidenceComplete || len(f.Hard) > 0 || len(f.HardRead) > 0 || len(f.Soft) > 0 || f.EffectiveMode != policy.ModeAuto || (f.Role != policy.RoleDev && f.Role != policy.RoleServer) || f.OpaqueExecution {
		return false
	}
	if f.Role == policy.RoleServer {
		for _, e := range f.Effects {
			if e == "edit-worktree" || e == "delete" || e == "service" || e == "irreversible" {
				return false
			}
		}
	}
	switch category {
	case "outside-worktree":
		return f.ReadOnlyProven && !f.WritesOutside
	case "exec-other":
		return f.BoundedReversible && !f.WritesOutside
	case "install":
		return f.InstallSource == "default" && f.BoundedReversible && !f.WritesOutside
	}
	return false
}

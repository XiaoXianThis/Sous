package task

import (
	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func answerKind(a store.Approval) string {
	for _, o := range a.Options {
		if a.Status == "decided" && o.OptionID == a.OptionID {
			return o.Kind
		}
	}
	return ""
}
func reusableApproval(a store.Approval) bool {
	f := a.Facts
	if a.TaskID == "" || aiNever[a.Category] || f == nil || f.Version != 1 || f.PolicyVersion != policy.PolicyVersion || !f.Reusable || f.OperationKey == "" || f.ParseStatus != "ok" || !f.EvidenceComplete || len(f.Hard) > 0 || policy.Restricted(f.EffectiveMode) {
		return false
	}
	if f.Role == policy.RoleServer {
		if f.OpaqueExecution {
			return false
		}
		for _, v := range f.Effects {
			if v == "edit-worktree" || v == "delete" || v == "service" || v == "irreversible" {
				return false
			}
		}
	}
	return true
}
func userAllowsAlways(history []store.Approval, a store.Approval) (store.Approval, bool) {
	if !reusableApproval(a) {
		return store.Approval{}, false
	}
	h, ok := userPrecedent(history, a)
	if !ok {
		return store.Approval{}, false
	}
	return h, h.Facts != nil && h.Facts.OperationKey == a.Facts.OperationKey && answerKind(h) == "allow_always" && reusableApproval(h)
}

package task

import (
	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"testing"
)

func TestMediumNeedsEveryIndependentFact(t *testing.T) {
	good := proto.PolicyFacts{Version: 1, PolicyVersion: policy.PolicyVersion, ParseStatus: "ok", Role: policy.RoleDev, EffectiveMode: policy.ModeAuto, EvidenceComplete: true, BoundedReversible: true, ReadOnlyProven: true, InstallSource: "default"}
	for _, cat := range []string{"exec-other", "outside-worktree", "install"} {
		if !eligibleMedium(&good, cat) {
			t.Fatal("valid candidate excluded", cat)
		}
		changes := []func(*proto.PolicyFacts){
			func(f *proto.PolicyFacts) { f.Version = 0 }, func(f *proto.PolicyFacts) { f.PolicyVersion = "old" }, func(f *proto.PolicyFacts) { f.ParseStatus = "fallback" }, func(f *proto.PolicyFacts) { f.EvidenceComplete = false },
			func(f *proto.PolicyFacts) { f.Hard = []string{"secret"} }, func(f *proto.PolicyFacts) { f.HardRead = []string{"config"} }, func(f *proto.PolicyFacts) { f.Soft = []string{"network"} }, func(f *proto.PolicyFacts) { f.OpaqueExecution = true },
			func(f *proto.PolicyFacts) { f.EffectiveMode = policy.ModeReadOnly }, func(f *proto.PolicyFacts) { f.WritesOutside = true }, func(f *proto.PolicyFacts) { f.Role = policy.RoleServer; f.Effects = []string{"delete"} },
		}
		for i, change := range changes {
			f := good
			change(&f)
			if eligibleMedium(&f, cat) {
				t.Fatalf("%s accepted missing fact %d", cat, i)
			}
		}
	}
	for _, cat := range []string{"delete", "service", "git-remote", "irreversible", "floor"} {
		if eligibleMedium(&good, cat) || eligibleMedium(nil, cat) {
			t.Fatal("invalid medium category", cat)
		}
	}
	for _, cat := range []string{"exec-other", "install"} {
		f := good
		f.BoundedReversible = false
		if eligibleMedium(&f, cat) {
			t.Fatal("unbounded medium", cat)
		}
	}
	f := good
	f.InstallSource = "unknown"
	if eligibleMedium(&f, "install") {
		t.Fatal("unknown registry")
	}
	f = good
	f.ReadOnlyProven = false
	if eligibleMedium(&f, "outside-worktree") {
		t.Fatal("unproven readonly")
	}
	if (ApprovalPolicy{}).AllowMedium {
		t.Fatal("medium enabled by default")
	}
}

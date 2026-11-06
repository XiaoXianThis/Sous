package prompts

import (
	"strings"
	"testing"
)

// The supervisor's prompt carries the judgment of decision 0022, and none of
// the presets it replaced.
func TestSupervisorPromptHasNoPresets(t *testing.T) {
	p := Supervisor(Env{ThreadKind: "main"})
	for _, want := range []string{
		"讨论还是动手", "拿不准就当讨论", "consult", "project_read", "不要复述",
		"place", "base_branch", "远端还没有的分支", "不要默认 main", "git_status", "拿不准就选用户当前检出的分支", "memory_save", "task_verdict",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	for _, gone := range []string{"永远不合并", "合并后才部署", "默认 wokspace", "自己 push 并开 PR"} {
		if strings.Contains(p, gone) {
			t.Errorf("the prompt still says %q", gone)
		}
	}
}

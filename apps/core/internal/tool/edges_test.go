package tool

import (
	"reflect"
	"testing"
)

func TestToolEdges(t *testing.T) {
	cases := map[string][]string{"task_create": {"supervisor>task"}, "project_get": {"supervisor>task"}, "git_status": {"supervisor>task"}, "consult": {"supervisor>task"}, "run_cancel": {"supervisor>task"}, "run_message": {"supervisor>task"}, "schedule_put": {"supervisor>trigger"}, "web_search": {"supervisor>web", "web>search"}, "web_fetch": {"supervisor>web", "web>search"}, "mcp_list": {"supervisor>mcp"}, "mcp_propose": {"supervisor>mcp"}, "mcp_withdraw": {"supervisor>mcp"}, "mcp__demo__tool": {"supervisor>mcp", "mcp>mcpserver:demo"}, "machine_look": {"supervisor>nodehub"}, "other": nil}
	for name, want := range cases {
		if got := toolEdges(name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", name, got)
		}
	}
}

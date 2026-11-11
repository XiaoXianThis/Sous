package sysmap

import "strings"

// EventGroups maps an event type prefix to the group the map shows.
// Every event type must start with one of these prefixes.
var EventGroups = []struct{ Prefix, Group string }{
	{"thread.", "对话"}, {"message.", "对话"}, {"topic.", "对话"},
	{"memory.", "记忆"}, {"usage.", "用量"},
	{"task.", "任务"}, {"project.", "项目"}, {"persona.", "预设"},
	{"run.", "Run"}, {"consult.", "Run"},
	{"node.", "机器"}, {"agent.job.", "机器"},
	{"approval.", "审批"},
	{"schedule.", "定时"}, {"trigger.", "定时"},
	{"inspector.", "巡检"}, {"notify.", "推送"},
	{"models.", "模型"}, {"mcp.", "MCP"},
	{"note.", "记事本"}, {"meeting.", "会议"}, {"minutes.", "会议"},
}

// GroupOf returns the group of an event type, "" when none matches.
func GroupOf(eventType string) string {
	for _, g := range EventGroups {
		if strings.HasPrefix(eventType, g.Prefix) {
			return g.Group
		}
	}
	return ""
}

// ReadEdges maps an event type to the read lines it lights.
var ReadEdges = map[string][]string{
	"message.user":       {"eventlog>supervisor"},
	"trigger.fired":      {"eventlog>supervisor"},
	"message.moved":      {"eventlog>supervisor"},
	"node.repos":         {"eventlog>task"},
	"node.online":        {"eventlog>task"},
	"node.inventory":     {"eventlog>task"},
	"run.ended":          {"eventlog>task"},
	"task.created":       {"eventlog>task"},
	"project.put":        {"eventlog>task"},
	"task.status":        {"eventlog>task"},
	"approval.requested": {"eventlog>task"},
	"approval.decided":   {"eventlog>task"},
	"run.paused":         {"eventlog>task"},
	"run.resumed":        {"eventlog>task"},
	"schedule.put":       {"eventlog>trigger"},
}

// FrameDown and FrameUp list the Hub ↔ Node frame types by direction.
var FrameDown = []string{"welcome", "ack", "run.start", "run.message", "run.cancel", "run.pause", "run.resume",
	"approval.decide", "workspace.remove", "node.update", "node.request"}
var FrameUp = []string{"hello", "inventory", "ack", "run.started", "run.update", "run.output", "run.diff", "run.ended",
	"run.turn", "run.delivered", "run.ask", "approval.request", "node.response", "node.stream", "node.job_ended",
	"node.repos", "node.update_result"}

package sysmap

import (
	"context"
	"fmt"
	"time"
)

type Graph struct {
	Modules []Module          `json:"modules"`
	Edges   []Edge            `json:"edges"`
	Status  map[string]Status `json:"status"`
	Now     int64             `json:"now"`
}

func Snapshot(ctx context.Context) Graph {
	g := Graph{Modules: append([]Module{}, Modules...), Edges: append([]Edge{}, StaticEdges...), Status: map[string]Status{}, Now: now().Unix()}
	reg.Lock()
	probes := make(map[string]Prober, len(reg.probes))
	for k, v := range reg.probes {
		probes[k] = v
	}
	instances := make(map[string]func(context.Context) []Instance, len(reg.instances))
	for k, v := range reg.instances {
		instances[k] = v
	}
	reg.Unlock()
	for _, kind := range []string{"provider", "mcpserver", "machine"} {
		if fn := instances[kind]; fn != nil {
			for _, inst := range fn(ctx) {
				id := inst.Module.ID
				g.Modules = append(g.Modules, inst.Module)
				g.Status[id] = inst.Status
				switch kind {
				case "provider":
					g.Edges = append(g.Edges, Edge{ID: EdgeID("models", id), From: "models", To: id, Label: "请求", Carries: "发给这个供应商的模型请求"})
				case "mcpserver":
					g.Edges = append(g.Edges, Edge{ID: EdgeID("mcp", id), From: "mcp", To: id, Label: "调用", Carries: "这个 MCP 服务的工具调用"})
				case "machine":
					g.Edges = append(g.Edges, Edge{ID: EdgeID("nodehub", id), From: "nodehub", To: id, Label: "下发", Carries: "Run 的开始、追加指令、取消、审批结果等帧"}, Edge{ID: EdgeID(id, "nodehub"), From: id, To: "nodehub", Label: "上报", Carries: "Run 的进展、输出、结束、审批请求等帧"})
				}
			}
		}
	}
	for _, m := range g.Modules {
		if p := probes[m.ID]; p != nil {
			g.Status[m.ID] = probe(ctx, p)
		} else if _, ok := g.Status[m.ID]; !ok {
			g.Status[m.ID] = Status{State: Idle}
		}
	}
	return g
}
func probe(ctx context.Context, p Prober) Status {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	ch := make(chan Status, 1)
	go func() {
		defer func() {
			if recover() != nil {
				ch <- Status{State: Degraded, Summary: "状态读取超时"}
			}
		}()
		ch <- p.Probe(ctx)
	}()
	select {
	case s := <-ch:
		return s
	case <-ctx.Done():
		return Status{State: Degraded, Summary: "状态读取超时"}
	}
}

// Ago formats elapsed time for status summaries.
func Ago(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(d/time.Hour))
	}
	return fmt.Sprintf("%d 天前", int(d/(24*time.Hour)))
}

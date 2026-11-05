package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// Tools returns the tools the agent uses to look at and extend its own MCP
// setup. The agent can only propose: a server it adds is pending until the
// user approves it on the page, and a pending server never starts.
func (m *Manager) Tools() []tool.Tool {
	return []tool.Tool{list{m}, propose{m}, withdraw{m}}
}

type list struct{ m *Manager }

func (list) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "mcp_list",
		Description: "查看已配置的 MCP 服务：名字、状态（ready 可用 / error 连不上 / off 未开启 / 等用户批准）、" +
			"工具名和出错原因。用户问“有哪些 MCP”、你想确认某个服务能不能用、或者你提议的服务被批准后连不上时用。",
		Schema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (t list) Call(ctx context.Context, _ json.RawMessage) (string, error) {
	vs, err := t.m.Views(ctx)
	if err != nil {
		return "", err
	}
	if len(vs) == 0 {
		return "还没有配置任何 MCP 服务。", nil
	}
	var b strings.Builder
	for _, v := range vs {
		state := v.State
		switch {
		case v.Pending:
			state = "等用户批准"
		case !v.Enabled:
			state = "已关闭"
		}
		fmt.Fprintf(&b, "- %s（%s，%s", v.Name, v.Transport, state)
		if v.Source == "agent" {
			b.WriteString("，你提议的")
		}
		b.WriteString(")")
		if v.Error != "" {
			fmt.Fprintf(&b, "\n  错误：%s", clip(v.Error, 300))
		}
		if len(v.Missing) > 0 {
			fmt.Fprintf(&b, "\n  还差用户填写：%s", strings.Join(v.Missing, "、"))
		}
		var names []string
		for _, tv := range v.Tools {
			if !tv.Disabled && tv.Exposed != "" {
				names = append(names, tv.Exposed)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "\n  工具：%s", strings.Join(names, "、"))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

type propose struct{ m *Manager }

func (propose) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "mcp_propose",
		Description: "提议添加一个 MCP 服务，扩展你自己的能力。你只能“提议”：服务会先保存为待批准，" +
			"用户在 设置 → MCP 里检查并批准后才会启动（本地程序形式的服务等于在 Hub 上运行命令，所以必须用户点头）。" +
			"提议之后要告诉用户去批准、为什么需要它。只提议你确定存在、来源可信的服务（官方文档里写的命令或地址），不要猜。" +
			"密钥（API Key、令牌）不要自己填：把变量名放进 secrets（环境变量）或 header_secrets（请求头），由用户在页面里填值。" +
			"批准后用 mcp_list 看它连上没有、有哪些工具；新工具在用户批准后的下一轮对话里可用。" +
			"两种形式：http（远程服务，填 url）和 stdio（在 Hub 上运行程序，填 command 和 args，如 command=npx、args=[\"-y\",\"包名\"]）。",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","description":"服务名，如 github、notion；英文小写更好"},` +
			`"transport":{"type":"string","enum":["http","stdio"]},` +
			`"url":{"type":"string","description":"http 形式：服务地址，https://…"},` +
			`"command":{"type":"string","description":"stdio 形式：可执行文件，如 npx、uvx；参数放 args"},` +
			`"args":{"type":"array","items":{"type":"string"}},` +
			`"env":{"type":"object","additionalProperties":{"type":"string"},"description":"stdio 的环境变量（不含密钥）"},` +
			`"secrets":{"type":"array","items":{"type":"string"},"description":"stdio 需要用户填值的环境变量名，如 GITHUB_TOKEN"},` +
			`"header_secrets":{"type":"array","items":{"type":"string"},"description":"http 需要用户填值的请求头名，如 Authorization"},` +
			`"reason":{"type":"string","description":"一句话：为什么要加它、用来做什么，用户批准时会看到"}},` +
			`"required":["name","transport","reason"]}`),
	}
}

func (t propose) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Name          string
		Transport     string
		URL           string
		Command       string
		Args          []string
		Env           map[string]string
		Secrets       []string
		HeaderSecrets []string `json:"header_secrets"`
		Reason        string
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Reason) == "" {
		return "", errors.New("需要 reason：告诉用户为什么要加它")
	}
	cfg := store.MCPServer{
		Name: a.Name, Transport: a.Transport, Command: a.Command, Args: a.Args, URL: a.URL,
		Env: map[string]string{}, Headers: map[string]string{},
		Enabled: true, Pending: true, Source: "agent", Note: clip(strings.TrimSpace(a.Reason), 500),
	}
	for k, v := range a.Env {
		cfg.Env[k] = v
	}
	for _, k := range a.Secrets {
		if _, ok := cfg.Env[k]; !ok {
			cfg.Env[k] = ""
		}
	}
	for _, k := range a.HeaderSecrets {
		cfg.Headers[k] = ""
	}
	saved, err := t.m.Add(ctx, cfg)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("已提议添加 MCP 服务「%s」，现在是待批准状态，还没有启动。请告诉用户：打开 设置 → MCP，检查后点“批准”", saved.Name)
	if miss := Missing(saved); len(miss) > 0 {
		msg += "（批准前要先填写：" + strings.Join(miss, "、") + "）"
	}
	return msg + "。批准后它的工具会出现在你的工具列表里。", nil
}

type withdraw struct{ m *Manager }

func (withdraw) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name:        "mcp_withdraw",
		Description: "撤回你提议的、用户还没批准的 MCP 服务（比如写错了要重新提议）。已批准的服务和用户自己加的服务你不能删，让用户在页面里处理。",
		Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"服务名"}},"required":["name"]}`),
	}
}

func (t withdraw) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Name string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	all, err := t.m.store().MCPServers(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range all {
		if Slug(s.Name) != Slug(a.Name) {
			continue
		}
		if !s.Pending || s.Source != "agent" {
			return "", errors.New("这个服务已经批准或者不是你提议的，你不能删除它；让用户在 设置 → MCP 里处理")
		}
		if err := t.m.Remove(ctx, s.ID); err != nil {
			return "", err
		}
		return "已撤回「" + s.Name + "」的提议。", nil
	}
	return "", errors.New("没有叫这个名字的 MCP 服务")
}

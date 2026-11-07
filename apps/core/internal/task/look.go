package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

// maxLooksPerTurn bounds machine_look in one supervisor turn: it is for a
// glance at the environment, not for doing the work (decision 0024).
const maxLooksPerTurn = 4

// lookTool lets the supervisor run one read-only command on a machine.
type lookTool struct {
	e  *Engine
	mu *sync.Mutex
	n  map[string]int // looks per turn
}

func newLookTool(e *Engine) lookTool {
	return lookTool{e: e, mu: &sync.Mutex{}, n: map[string]int{}}
}

func (lookTool) Spec() provider.ToolSpec {
	return provider.ToolSpec{
		Name: "machine_look",
		Description: "在用户的一台电脑上执行一条只读的查看命令，看一眼环境：进程、端口、磁盘、版本、服务状态、日志的最后几行、某个目录里有什么。" +
			"只用来帮你判断和回答（“服务起来了吗”“装了什么版本”“磁盘还剩多少”），不是用来干活的：" +
			"改东西、装东西、跑构建测试、要连着查很多步的排查和调研，都建任务交给编程 agent。每轮最多用 " + fmt.Sprint(maxLooksPerTurn) + " 次。" +
			"命令由那台电脑按只读规则检查，会改动、联网发送、读密钥或环境变量的命令会被拒绝。",
		Schema: json.RawMessage(`{"type":"object","properties":{
			"machine":{"type":"string","description":"电脑名或 id；给了 project 时可以不填（用项目所在的电脑）"},
			"project":{"type":"string","description":"在这个项目的目录里执行；项目对话里可以不填"},
			"dir":{"type":"string","description":"在哪个目录执行（绝对路径或 ~/…）；不填是项目目录，没有项目时是家目录"},
			"command":{"type":"string","description":"一条只读命令，比如 df -h、ps aux | grep node、tail -n 50 /var/log/x.log、node --version"}
		},"required":["command"]}`),
	}
}

func (t lookTool) Call(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Machine, Project, Dir, Command string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	turn := tool.TurnFrom(ctx)
	key := fmt.Sprintf("%s#%d", turn.ThreadID, turn.ReplyTo)
	t.mu.Lock()
	if len(t.n) > 200 {
		clear(t.n)
	}
	t.n[key]++
	n := t.n[key]
	t.mu.Unlock()
	if n > maxLooksPerTurn {
		return "", fmt.Errorf("%w: 这一轮已经看了 %d 次；要继续查就建一个任务交给编程 agent，或者先根据已有的信息回答", ErrConflict, maxLooksPerTurn)
	}
	node, dir := "", strings.TrimSpace(a.Dir)
	if a.Machine != "" {
		id, err := t.e.findNode(ctx, a.Machine)
		if err != nil {
			return "", err
		}
		node = id
	}
	if a.Project != "" || (scopeOf(ctx) != "" && a.Machine == "") {
		p, _, err := t.e.projectAndNode(ctx, a.Project, "")
		if err != nil {
			return "", err
		}
		place, err := t.e.readablePlace(ctx, p, node)
		if err != nil {
			return "", err
		}
		node = place.NodeID
		if dir == "" {
			dir = place.Path
		}
	}
	if node == "" {
		return "", fmt.Errorf("%w: 要指定电脑（machine）或项目（project）", ErrInvalid)
	}
	res, err := t.e.Nodes.Look(ctx, node, proto.LookArgs{Dir: dir, Command: a.Command})
	if err != nil {
		return "", nodeError(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s 上 `%s` 的输出（退出码 %d）：\n%s", t.e.nodeName(ctx, node), a.Command, res.ExitCode, truncate(res.Output, maxReadRunes))
	if res.Truncated {
		b.WriteString("\n…输出太长，只保留了前面的部分")
	}
	if res.TimedOut {
		b.WriteString("\n…命令超时，被停下了")
	}
	return b.String(), nil
}

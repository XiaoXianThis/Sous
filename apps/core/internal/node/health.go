package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

// healthTimeout bounds one health check.
const healthTimeout = 2 * time.Minute

// healthOp runs a deploy's health check in the deploy directory: the
// project's own command, through the policy like any command. A deploy is
// only recorded as done when it passes (end-to-end review 4.1): an agent's
// summary is not proof that anything was deployed.
func (r *run) healthOp(ctx context.Context, op *proto.GitOp) (proto.GitResult, error) {
	res := proto.GitResult{Op: proto.GitHealth}
	dir := r.spec.RepoPath
	r.markStarted(workspace.Prepared{Dir: dir})
	cmd := strings.TrimSpace(op.Command)
	if cmd == "" {
		return res, errors.New("项目没有配置健康检查")
	}
	if err := r.allow("运行部署后的健康检查", cmd, dir, "在 "+dir+" 执行项目设置里的健康检查命令："+cmd); err != nil {
		return res, err
	}
	r.status("正在做健康检查：" + cmd)
	out, code, err := runLine(ctx, dir, cmd)
	tail := lastRunes(mask(strings.TrimSpace(out)), 600)
	if err != nil && code < 0 {
		return res, fmt.Errorf("健康检查没能运行：%w", err)
	}
	if code != 0 {
		res.Message = fmt.Sprintf("健康检查没有通过（退出码 %d）", code)
		if tail != "" {
			res.Message += "：" + tail
		}
		return res, errors.New(res.Message)
	}
	res.Message = "健康检查通过"
	if tail != "" {
		res.Message += "：" + tail
	}
	return res, nil
}

// runLine runs one command line with the user's shell and returns its
// combined output and exit code (-1 when it did not run).
func runLine(ctx context.Context, dir, line string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	sh, args := shellCommand()
	switch name := strings.ToLower(filepath.Base(sh)); {
	case runtime.GOOS != "windows":
		args = append(args, "-c", line)
	case strings.HasPrefix(name, "pwsh") || strings.HasPrefix(name, "powershell"):
		args = append(args, "-NonInteractive", "-Command", line)
	default:
		args = append(args, "/C", line)
	}
	cmd := exec.CommandContext(ctx, sh, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return out.String(), ee.ExitCode(), nil
	}
	if ctx.Err() != nil {
		return out.String(), -1, errors.New("超时")
	}
	return out.String(), -1, err
}

// lastRunes keeps the end of s: where a command says why it failed.
func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

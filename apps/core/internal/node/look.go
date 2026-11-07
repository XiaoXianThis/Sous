package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// Limits of machine.look: a quick glance, not a job.
const (
	lookTimeout = 20 * time.Second
	lookBytes   = 16 << 10
)

// secretDump matches commands that print the environment or secret files:
// read-only, but their output would go into the supervisor's context.
var secretDump = regexp.MustCompile(`(^|[\s;&|(])(env|printenv|set|export|declare)\s*($|[;&|)])|(^|[\s/="'])\.env(\.[\w-]+)?($|[\s"'])`)

// look runs one command the policy calls read-only (decision 0024: the
// supervisor's machine_look). The read-only mode of the policy decides,
// on this machine; anything it does not allow is refused, never asked.
func (a *admin) look(ctx context.Context, args proto.LookArgs) (proto.LookResult, error) {
	cmd := strings.TrimSpace(args.Command)
	if cmd == "" {
		return proto.LookResult{}, nodeErr(proto.ErrCodeInvalid, "没有命令")
	}
	dir := ExpandPath(args.Dir)
	if dir == "" {
		dir = ExpandPath("~")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return proto.LookResult{}, nodeErr(proto.ErrCodeNotFound, "目录 %s 不存在", args.Dir)
	}
	raw, _ := json.Marshal(map[string]string{"command": cmd})
	req := policy.Request{ToolKind: "execute", Title: cmd, RawInput: raw, Workdir: dir}
	pol := a.d.pol()
	cat, why := pol.Classify(req)
	act, reason := policy.Judge(a.d.role(), policy.ModeReadOnly, cat, pol.Assess(req, cat))
	if act != policy.Allow {
		return proto.LookResult{}, nodeErr(proto.ErrCodeInvalid, "machine_look 只能执行只读的查看命令，这条不行（%s）%s", cat, reasonSuffix(firstNonEmpty(reason, why)))
	}
	if secretDump.MatchString(cmd) {
		return proto.LookResult{}, nodeErr(proto.ErrCodeInvalid, "machine_look 不读环境变量和密钥文件，它们会进到对话里")
	}
	slog.Info("daemon: machine.look", "dir", dir, "command", cmd)
	ctx, cancel := context.WithTimeout(ctx, lookTimeout)
	defer cancel()
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", cmd)
	} else {
		c = exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	}
	c.Dir = dir
	c.Stdin = nil
	out := &capped{max: lookBytes}
	c.Stdout, c.Stderr = out, out
	err := c.Run()
	res := proto.LookResult{Output: strings.ToValidUTF8(out.b.String(), "�"), Truncated: out.cut, TimedOut: ctx.Err() != nil}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	case err != nil && !res.TimedOut:
		return res, nodeErr(proto.ErrCodeFailed, "%v", err)
	}
	return res, nil
}

// capped keeps the first max bytes written to it.
type capped struct {
	b   bytes.Buffer
	max int
	cut bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); room < len(p) {
		if room > 0 {
			c.b.Write(p[:room])
		}
		c.cut = true
		return len(p), nil
	}
	return c.b.Write(p)
}

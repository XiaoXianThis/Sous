//go:build !windows

package acp

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the agent in its own process group, so Close can
// stop the helpers it starts (npx, shells, language servers) too.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	cmd.Process.Kill()
}

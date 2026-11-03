//go:build windows

package acp

import (
	"os/exec"
	"strconv"
)

func configureProcess(cmd *exec.Cmd) {}

// killProcess ends the agent and its child processes.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	cmd.Process.Kill()
}

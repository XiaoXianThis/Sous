//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// hideWindow keeps a console window from flashing up for the daemon.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// stopProcess ends the daemon; Windows has no SIGTERM for a windowless child.
func stopProcess(p *os.Process) { p.Kill() }

//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

// stopProcess asks the daemon to stop cleanly (it snapshots the brain first).
func stopProcess(p *os.Process) { p.Signal(syscall.SIGTERM) }

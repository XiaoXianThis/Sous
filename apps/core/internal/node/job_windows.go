//go:build windows

package node

import (
	"os"
	"os/exec"
	"strconv"
)

// killTree ends a job and the processes it started.
func killTree(p *os.Process) {
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run() != nil {
		p.Kill()
	}
}

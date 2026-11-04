//go:build !windows

package node

import (
	"os"
	"syscall"
)

// killTree ends a job and everything it started: the pseudo-terminal made
// it a session leader, so its process group holds its children.
func killTree(p *os.Process) {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		p.Kill()
	}
}

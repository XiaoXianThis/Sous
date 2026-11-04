//go:build !windows

package node

import (
	"os"
	"os/exec"
)

// shellCommand is the user's login shell ($SHELL), so PATH and the prompt
// are what they get in their own terminal.
func shellCommand() (string, []string) {
	if sh := os.Getenv("SHELL"); sh != "" {
		if p, err := exec.LookPath(sh); err == nil {
			return p, []string{"-l"}
		}
	}
	for _, sh := range []string{"/bin/zsh", "/bin/bash"} {
		if _, err := os.Stat(sh); err == nil {
			return sh, []string{"-l"}
		}
	}
	return "/bin/sh", nil
}

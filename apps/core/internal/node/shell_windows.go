//go:build windows

package node

import (
	"os"
	"os/exec"
)

// shellCommand prefers PowerShell 7, then Windows PowerShell, then cmd.
func shellCommand() (string, []string) {
	for _, sh := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p, []string{"-NoLogo"}
		}
	}
	if c := os.Getenv("COMSPEC"); c != "" {
		return c, nil
	}
	return "cmd.exe", nil
}

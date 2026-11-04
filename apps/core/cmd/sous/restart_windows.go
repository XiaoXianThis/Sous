//go:build windows

package main

import (
	"os"
	"os/exec"
)

// execSelf starts the (new) executable with the same arguments and exits;
// Windows has no exec.
func execSelf() error {
	cmd := exec.Command(exePath(), os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

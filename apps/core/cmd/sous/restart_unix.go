//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execSelf replaces the process with the (new) executable, same arguments.
func execSelf() error {
	return syscall.Exec(exePath(), os.Args, os.Environ())
}

//go:build !windows

package node

import (
	"os"
	"syscall"
)

func openSnapshot(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

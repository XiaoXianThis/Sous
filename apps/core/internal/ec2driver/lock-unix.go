//go:build linux || darwin

package ec2driver

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("EC2 ledger is locked by another reconciliation")
	}
	return nil
}

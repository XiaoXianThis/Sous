//go:build !linux && !darwin

package ec2driver

import (
	"errors"
	"os"
)

func lockFile(*os.File) error {
	return errors.New("EC2 driver requires a Linux or macOS controller host")
}

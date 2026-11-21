// sous-ec2-driver is run by the separate capacity controller. The default
// mode accepts one request on stdin; secrets never appear in command args.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ec2driver"
)

func run() error {
	if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Fprintln(os.Stdout, "Set SOUS_EC2_DRIVER_CONFIG to a private operator JSON config. Commands: init-ledger, status, verify; no args reads a controller request from stdin.")
		return nil
	}
	c, err := ec2driver.ReadConfig(os.Getenv("SOUS_EC2_DRIVER_CONFIG"))
	if err != nil {
		return err
	}
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "init-ledger":
			return ec2driver.Init(c)
		case "verify":
			ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
			defer cancel()
			if err := ec2driver.Verify(ctx, c); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "EC2 account, AMI, and network checks passed (no instance launched)")
			return nil
		case "status":
			s, err := ec2driver.Status(c)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(s)
		}
	}
	if len(os.Args) != 1 {
		return errors.New("unexpected EC2 driver arguments")
	}
	r, err := ec2driver.ReadRequest(os.Stdin)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
	defer cancel()
	state, err := ec2driver.Run(ctx, c, r)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"state": state})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sous-ec2-driver:", err)
		os.Exit(1)
	}
}

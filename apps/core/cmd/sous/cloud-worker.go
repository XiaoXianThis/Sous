package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudworker"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
)

func runCloudWorker(args []string) error {
	if len(args) > 0 && args[0] == "prepare-image" {
		f := flag.NewFlagSet("sous cloud-worker prepare-image", flag.ContinueOnError)
		out := f.String("output", "", "new absolute file for the pinned OCI image archive")
		if err := f.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if f.NArg() != 0 || *out == "" {
			return errors.New("cloud-worker prepare-image needs an explicit output file")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return cloudworker.PrepareImage(ctx, *out)
	}
	f := flag.NewFlagSet("sous cloud-worker", flag.ContinueOnError)
	origin := f.String("hub", "", "canonical HTTPS workspace origin")
	tokenFile := f.String("token-file", "", "private worker token file (one-use bootstrap when --generation is set)")
	generation := f.Int64("generation", 0, "managed cloud capacity generation")
	ca := f.String("ca-file", "", "optional private CA certificate")
	socket := f.String("connect-loopback", "", "optional numeric loopback socket, preserving TLS identity")
	var o cloudworker.Options
	f.StringVar(&o.State, "state", "", "private durable worker checkpoint directory")
	f.StringVar(&o.Runtime, "runtime-home", "", "private short Microsandbox runtime directory")
	f.StringVar(&o.MSB, "msb", "", "absolute verified Microsandbox executable")
	f.StringVar(&o.Firmware, "firmware", "", "absolute verified Microsandbox firmware")
	f.StringVar(&o.Image, "image-archive", "", "absolute approved offline OCI image")
	f.BoolVar(&o.Once, "once", false, "process at most one authorized job, then exit")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || (*generation == 0 && *tokenFile == "") {
		return errors.New("cloud-worker needs credentials (static mode requires a token file) and explicit runner paths")
	}
	var token string
	if *tokenFile != "" {
		_, statErr := os.Lstat(*tokenFile)
		if *generation <= 0 || !errors.Is(statErr, os.ErrNotExist) {
			var err error
			token, err = config.ReadWorkerToken(*tokenFile)
			if err != nil {
				return err
			}
		}
	}
	c, err := cloudworker.NewClient(*origin, token, *ca, *socket, *generation)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cloudworker.Run(ctx, c, o)
}

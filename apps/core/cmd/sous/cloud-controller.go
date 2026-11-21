package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudcontroller"
	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
)

func runCloudController(args []string) error {
	f := flag.NewFlagSet("sous cloud-controller", flag.ContinueOnError)
	origin := f.String("hub", "", "canonical HTTPS workspace origin")
	tokenFile := f.String("token-file", "", "private controller bearer file")
	seedFile := f.String("bootstrap-seed-file", "", "different private bootstrap derivation seed")
	provider := f.String("provider", "", "absolute operator-owned capacity driver executable")
	ca := f.String("ca-file", "", "optional private CA certificate")
	socket := f.String("connect-loopback", "", "optional numeric loopback socket preserving TLS identity")
	once := f.Bool("once", false, "perform at most one reconciliation tick")
	interval := f.Duration("interval", 2*time.Second, "reconcile interval (1s–1m)")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *tokenFile == "" || *seedFile == "" || *interval < time.Second || *interval > time.Minute {
		return errors.New("cloud-controller requires distinct credential files and an explicit provider")
	}
	token, err := config.ReadWorkerToken(*tokenFile)
	if err != nil {
		return err
	}
	seed, err := config.ReadWorkerToken(*seedFile)
	if err != nil {
		return err
	}
	h, err := cloudcontroller.NewClient(*origin, token, *ca, *socket)
	if err != nil {
		return err
	}
	defer h.Close()
	p, err := cloudcontroller.NewDriver(*provider)
	if err != nil {
		return err
	}
	c, err := cloudcontroller.New(h, p, seed)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var lastPhase string
	var lastGeneration int64 = -1
	for ctx.Err() == nil {
		state, err := c.Step(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("cloud capacity reconciliation not confirmed", "error", err)
		}
		if err == nil && (lastPhase != state.Phase || lastGeneration != state.Generation) {
			slog.Info("cloud capacity reconciled", "phase", state.Phase, "generation", state.Generation)
			lastPhase, lastGeneration = state.Phase, state.Generation
		}
		if *once {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*interval):
		}
	}
	return nil
}

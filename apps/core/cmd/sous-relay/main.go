// Command sous-relay is the server of sous 0.3 (decision 0030): a blind
// relay that forwards and stores ciphertext and signed documents. It holds
// no key that matters: if it is broken into, the attacker can stop
// messages and see who talks when, nothing more.
//
//	sous-relay init  [-data DIR]                          print a one-time claim token
//	sous-relay serve [-data DIR] [-listen ADDR] [-origin URL] [-tls-cert F -tls-key F]
//	sous-relay version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/relay"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sous-relay:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	data := fs.String("data", envOr("SOUS_RELAY_DATA", "data"), "data directory")
	listen := fs.String("listen", envOr("SOUS_RELAY_LISTEN", "127.0.0.1:8788"), "address to listen on")
	origins := fs.String("origin", os.Getenv("SOUS_RELAY_ORIGIN"), "the page's origin(s), comma-separated (e.g. https://sous.example.com)")
	cert := fs.String("tls-cert", "", "TLS certificate (omit behind a TLS proxy)")
	key := fs.String("tls-key", "", "TLS key")
	debug := fs.Bool("debug", false, "log debug messages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	switch cmd {
	case "version":
		fmt.Println(version.Version)
		return nil
	case "init":
		s, err := relay.Open(*data)
		if err != nil {
			return err
		}
		defer s.Close()
		tok, err := s.Init()
		if err != nil {
			return err
		}
		fmt.Println("认领码（只能用一次，在页面的首次设置里填写）：")
		fmt.Println(tok)
		return nil
	case "serve":
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	s, err := relay.Open(*data)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			s.Origins = append(s.Origins, o)
		}
	}
	if !s.Claimed() {
		slog.Warn("relay: not claimed yet; run `sous-relay init` and use the token in the page's first setup")
	}
	srv := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		if *cert != "" {
			errc <- srv.ListenAndServeTLS(*cert, *key)
			return
		}
		errc <- srv.ListenAndServe()
	}()
	slog.Info("sous-relay listening", "addr", *listen, "origins", s.Origins, "version", version.Version)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

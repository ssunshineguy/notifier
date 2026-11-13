// Command notifier runs one of the Notifier modes.
//
// Usage:
//
//	notifier [-config path] <mode>
//
// Modes: api, worker, collector, migrate.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ssunshineguy/notifier/internal/application"
	"github.com/ssunshineguy/notifier/internal/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("notifier failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "configs/config.yaml", "path to the config file")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		// Nothing to recover from if usage cannot be printed.
		_, _ = fmt.Fprintln(out, "Usage: notifier [-config path] <api|worker|collector|migrate>")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		return errors.New("exactly one mode is required")
	}
	mode := flag.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	switch mode {
	case "migrate":
		return application.Migrate(ctx, cfg)
	case "api":
		return application.RunAPI(ctx, cfg)
	case "worker", "collector":
		return fmt.Errorf("mode %q is not implemented yet", mode)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

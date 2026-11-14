// Command notifier runs one of the Notifier modes.
//
// Usage:
//
//	notifier [-config path] [-listen-addr address] <mode>
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

	"github.com/ssunshineguy/notifier/notifier/internal/application"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func main() {
	if err := runWithLogging(); err != nil {
		os.Exit(1)
	}
}

func runWithLogging() error {
	level := slog.LevelInfo
	if value := os.Getenv("NOTIFIER_LOG_LEVEL"); value != "" {
		if err := level.UnmarshalText([]byte(value)); err != nil {
			err := errors.New("invalid NOTIFIER_LOG_LEVEL")
			slog.Error("configure logging", "error", err)
			return err
		}
	}
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	err := run()
	if err != nil {
		slog.Error("notifier failed", "error_type", fmt.Sprintf("%T", err))
	}
	return err
}

func run() error {
	configPath := flag.String("config", "notifier/configs/config.yaml", "path to the config file")
	listenAddr := flag.String("listen-addr", "", "override the API listen address")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		// Nothing to recover from if usage cannot be printed.
		_, _ = fmt.Fprintln(out, "Usage: notifier [-config path] [-listen-addr address] <api|worker|collector|migrate>")
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
		if *listenAddr != "" {
			cfg.API.ListenAddr = *listenAddr
		}
		return application.RunAPI(ctx, cfg)
	case "worker":
		return application.RunWorker(ctx, cfg)
	case "collector":
		return fmt.Errorf("mode %q is not implemented yet", mode)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

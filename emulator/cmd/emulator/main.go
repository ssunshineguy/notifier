// Command emulator serves the local Notifier test emulator.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ssunshineguy/notifier/emulator/internal/application"
)

func main() {
	addr := flag.String("listen-addr", "127.0.0.1:8081", "emulator listen address")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := application.Run(ctx, *addr); err != nil {
		slog.Error("emulator failed", "error", err)
		os.Exit(1)
	}
}

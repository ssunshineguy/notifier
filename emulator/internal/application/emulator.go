// Package application owns the emulator's HTTP server lifecycle.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/ssunshineguy/notifier/emulator/internal/server/codegen"
	"github.com/ssunshineguy/notifier/emulator/internal/server/handlers"
	"github.com/ssunshineguy/notifier/emulator/internal/storage/memory"
	"github.com/ssunshineguy/notifier/internal/health"
	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/problem"
)

var loggableRequestID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// NewRouter creates the emulator's HTTP router with an empty override store.
func NewRouter() (http.Handler, error) {
	return codegen.NewServer(handlers.New(memory.New()), codegen.WithErrorHandler(handleError))
}

func handleError(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID != "" && !loggableRequestID.MatchString(requestID) {
		requestID = "[redacted]"
	}
	ctx = logging.WithLogger(ctx, slog.Default().With("request_id", requestID))
	problem.WriteError(ctx, w, r, ogenerrors.ErrorCode(err))
}

// Run serves the emulator until ctx is canceled or the server fails.
func Run(ctx context.Context, addr string) error {
	router, err := NewRouter()
	if err != nil {
		return fmt.Errorf("create emulator router: %w", err)
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		IdleTimeout:       time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	if err := health.Serve(ctx, server, "9081", health.NewHandler(nil)); err != nil {
		return fmt.Errorf("serve emulator: %w", err)
	}
	return nil
}

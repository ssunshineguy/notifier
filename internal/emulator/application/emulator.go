// Package application owns the emulator's HTTP server lifecycle.
package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/ssunshineguy/notifier/internal/emulator/server/codegen"
	"github.com/ssunshineguy/notifier/internal/emulator/server/handlers"
	"github.com/ssunshineguy/notifier/internal/emulator/storage/memory"
	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/server/middleware"
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
	middleware.HandleError(ctx, w, r, err)
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
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve emulator: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down emulator: %w", errors.Join(err, server.Close()))
		}
		return nil
	}
}

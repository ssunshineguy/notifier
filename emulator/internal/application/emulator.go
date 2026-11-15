// Package application owns the emulator's HTTP server lifecycle.
package application

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/ogen-go/ogen/ogenerrors"

	"github.com/ssunshineguy/notifier/emulator/internal/server/codegen"
	"github.com/ssunshineguy/notifier/emulator/internal/server/handlers"
	"github.com/ssunshineguy/notifier/emulator/internal/storage/memory"
	"github.com/ssunshineguy/notifier/internal/bodylimit"
	"github.com/ssunshineguy/notifier/internal/health"
	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/problem"
)

const (
	responseWriteTimeout = time.Second
	maxCallbackDelay     = time.Duration(math.MaxInt32) * time.Millisecond
)

var loggableRequestID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// NewRouter creates the emulator's HTTP router with fresh preset caches.
func NewRouter() (http.Handler, error) {
	router, err := codegen.NewServer(
		handlers.New(memory.NewClientCache(), memory.NewCallbackCache()),
		codegen.WithErrorHandler(handleError),
	)
	if err != nil {
		return nil, err
	}
	return bodylimit.Requests(router), nil
}

func handleError(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID != "" && !loggableRequestID.MatchString(requestID) {
		requestID = "[redacted]"
	}
	ctx = logging.WithLogger(ctx, slog.Default().With("request_id", requestID))
	status := ogenerrors.ErrorCode(err)
	if bodylimit.IsExceeded(err) {
		status = http.StatusRequestEntityTooLarge
	}
	problem.WriteError(ctx, w, r, status)
}

// Run serves the emulator until ctx is canceled or the server fails.
func Run(ctx context.Context, addr string) error {
	router, err := NewRouter()
	if err != nil {
		return fmt.Errorf("create emulator router: %w", err)
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           responseTimeout(responseWriteTimeout, router),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		// DND: Allow the full int32 callback delay; the response write budget starts at its first write.
		WriteTimeout: maxCallbackDelay + 5*time.Second + responseWriteTimeout,
		IdleTimeout:  time.Minute,
		BaseContext:  func(net.Listener) context.Context { return ctx },
	}
	if err := health.Serve(ctx, server, "9081", health.NewHandler(nil), 0); err != nil {
		return fmt.Errorf("serve emulator: %w", err)
	}
	return nil
}

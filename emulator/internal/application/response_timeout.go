package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func responseTimeout(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := &timedResponse{ResponseWriter: w, ctx: r.Context(), timeout: timeout}
		defer response.start()
		next.ServeHTTP(response, r)
	})
}

type timedResponse struct {
	http.ResponseWriter
	ctx     context.Context
	timeout time.Duration
	started bool
}

func (w *timedResponse) start() {
	if w.started {
		return
	}
	w.started = true
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(w.timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.DebugContext(w.ctx, "set response write deadline", "error_type", fmt.Sprintf("%T", err))
	}
}

// WriteHeader starts the response budget before forwarding the status.
func (w *timedResponse) WriteHeader(status int) {
	w.start()
	w.ResponseWriter.WriteHeader(status)
}

// Write starts the same response budget when no status was written.
func (w *timedResponse) Write(data []byte) (int, error) {
	w.start()
	return w.ResponseWriter.Write(data)
}

// Unwrap keeps ResponseController access to the underlying connection.
func (w *timedResponse) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

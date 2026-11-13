// Package logging carries request log attributes across service layers.
package logging

import (
	"context"
	"log/slog"
)

type loggerKey struct{}

// WithLogger attaches the request logger for downstream operations.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext returns the request logger or the process default outside a request.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/logging"
)

const selectClient = `
SELECT client_id, callback_url, max_lifetime_sec, callback_timeout_ms, created_dt
FROM notifier.t_client
WHERE client_id = $1`

// GetClient returns core.ErrClientNotFound when clientID is absent.
func (s *Store) GetClient(ctx context.Context, clientID string) (core.Client, error) {
	var client core.Client
	var maxLifetimeSec int32
	var callbackTimeoutMS *int32
	err := s.scanRow(ctx, "select_client", selectClient, []any{clientID}, func() slog.Attr {
		return slog.Group("client",
			logging.Redacted("client_id"), logging.Redacted("callback_url"),
			slog.Int64("max_lifetime_sec", int64(maxLifetimeSec)), slog.Any("callback_timeout_ms", callbackTimeoutMS))
	},
		&client.ID,
		&client.CallbackURL,
		&maxLifetimeSec,
		&callbackTimeoutMS,
		&client.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Client{}, core.ErrClientNotFound
	}
	if err != nil {
		return core.Client{}, fmt.Errorf("query client: %w", err)
	}

	client.MaxLifetime = time.Duration(maxLifetimeSec) * time.Second
	if callbackTimeoutMS != nil {
		timeout := time.Duration(*callbackTimeoutMS) * time.Millisecond
		client.CallbackTimeout = &timeout
	}
	return client, nil
}

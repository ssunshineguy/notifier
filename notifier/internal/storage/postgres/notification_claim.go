package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

const claimNotifications = `
WITH due AS (
    SELECT id
    FROM notifier.t_notification
    WHERE stop_dt IS NULL AND next_scheduled_dt <= now()
      AND (lock_until_dt IS NULL OR lock_until_dt <= now())
    ORDER BY next_scheduled_dt, id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE notifier.t_notification AS notification
SET lock_until_dt = statement_timestamp() + $2::bigint * interval '1 millisecond'
FROM due
WHERE notification.id = due.id
RETURNING notification.id, notification.lock_until_dt`

// ClaimNotifications reserves due rows atomically and returns their IDs and reservation deadlines.
func (s *Store) ClaimNotifications(ctx context.Context, limit int, lockDuration time.Duration) ([]core.NotificationClaim, error) {
	const queryName = "claim_notifications"
	start := time.Now()
	dbCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()

	logger := logging.FromContext(ctx).With("query_name", queryName)
	logger.InfoContext(ctx, "Start SQL "+queryName, "argument_count", 2)
	rows, err := s.pool.Query(dbCtx, claimNotifications, limit, lockDuration.Milliseconds())
	var claims []core.NotificationClaim
	if err == nil {
		claims, err = pgx.CollectRows(rows, pgx.RowToStructByPos[core.NotificationClaim])
	}

	durationMS := time.Since(start).Milliseconds()
	message := fmt.Sprintf("End SQL %s duration %d ms", queryName, durationMS)
	logger = logger.With("duration_ms", durationMS)
	switch {
	case err != nil:
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			logger.InfoContext(ctx, message, "outcome", "error", "sqlstate", postgresError.Code)
		} else {
			logger.InfoContext(ctx, message, "outcome", "error", "error_type", fmt.Sprintf("%T", err))
		}
		return nil, fmt.Errorf("claim notifications: %w", err)
	case len(claims) == 0:
		logger.InfoContext(ctx, message, "outcome", "no_rows", "row_count", 0)
	default:
		logger.InfoContext(ctx, message, "outcome", "ok", "row_count", len(claims))
	}
	return claims, nil
}

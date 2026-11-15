package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

const selectClaimedCallbackTarget = `
WITH reserved AS (
    SELECT client_id, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND stop_dt IS NULL AND lock_until_dt = $2
    FOR UPDATE
)
SELECT client.callback_url, client.callback_timeout_ms
FROM reserved
LEFT JOIN notifier.t_client AS client ON client.client_id = reserved.client_id
WHERE reserved.lock_until_dt > clock_timestamp()`

// GetClaimedCallbackTarget reads the callback destination while checking the reservation at delivery start.
func (s *Store) GetClaimedCallbackTarget(ctx context.Context, claim core.NotificationClaim) (core.CallbackTarget, error) {
	var url *string
	var timeoutMS *int32
	err := s.scanRow(ctx, "select_claimed_callback_target", selectClaimedCallbackTarget,
		[]any{claim.ID, claim.LockUntil}, func() slog.Attr { return slog.String("notification_id", claim.ID.String()) }, &url, &timeoutMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.CallbackTarget{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.CallbackTarget{}, fmt.Errorf("read claimed callback target: %w", err)
	}
	if url == nil {
		return core.CallbackTarget{}, core.ErrClientNotFound
	}
	target := core.CallbackTarget{URL: *url}
	if timeoutMS != nil {
		timeout := time.Duration(*timeoutMS) * time.Millisecond
		target.Timeout = &timeout
	}
	return target, nil
}

const updateNotificationResult = `
WITH reserved AS (
    SELECT id, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND stop_dt IS NULL AND lock_until_dt = $2
    FOR UPDATE
)
UPDATE notifier.t_notification AS notification
SET next_scheduled_dt = $3,
    failed_attempt_count = $4,
    notification_status = $5,
    delivery_status = NULLIF($6::text, ''),
    stop_dt = CASE WHEN $6::text IN ('delivered', 'delivery_failed') THEN clock_timestamp() END,
    lock_until_dt = NULL
FROM reserved
WHERE notification.id = reserved.id AND reserved.lock_until_dt > clock_timestamp()
RETURNING notification.id`

// SaveNotificationResult releases the claim atomically with the result or reports a lost claim.
func (s *Store) SaveNotificationResult(ctx context.Context, claim core.NotificationClaim, result core.NotificationResult) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "update_notification_result", updateNotificationResult,
		[]any{claim.ID, claim.LockUntil, result.NextScheduled, result.FailedAttemptCount, result.NotificationStatus, result.DeliveryStatus},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("save notification result: %w", err)
	}
	return nil
}

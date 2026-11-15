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

const moveNotificationToDLQ = `
WITH reserved AS (
    SELECT notification.*
    FROM notifier.t_notification AS notification
    WHERE id = $1 AND stop_dt IS NULL AND notification_status = 'active' AND lock_until_dt = $2
    FOR UPDATE
), transition AS (
    SELECT id, to_jsonb(reserved) AS original, clock_timestamp() AS stopped_at
    FROM reserved
    WHERE lock_until_dt > clock_timestamp()
), stopped AS (
    UPDATE notifier.t_notification AS notification
    SET notification_status = 'dlq', delivery_status = NULL,
        stop_dt = transition.stopped_at, next_scheduled_dt = NULL, lock_until_dt = NULL
    FROM transition
    WHERE notification.id = transition.id
    RETURNING notification.id
)
INSERT INTO notifier.t_dlq (notification_id, notification, reason, created_dt)
SELECT stopped.id, transition.original, $3, transition.stopped_at
FROM stopped JOIN transition USING (id)
RETURNING notification_id`

// MoveNotificationToDLQ archives the original row and stops its active claim atomically.
func (s *Store) MoveNotificationToDLQ(ctx context.Context, claim core.NotificationClaim) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "move_notification_to_dlq", moveNotificationToDLQ,
		[]any{claim.ID, claim.LockUntil, core.DLQInvalidStoredConditions},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("move notification to dlq: %w", err)
	}
	return nil
}

const rescheduleNotification = `
WITH reserved AS (
    SELECT id, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND stop_dt IS NULL AND lock_until_dt = $2
    FOR UPDATE
)
UPDATE notifier.t_notification AS notification
SET next_scheduled_dt = $3, lock_until_dt = NULL
FROM reserved
WHERE notification.id = reserved.id AND reserved.lock_until_dt > clock_timestamp()
RETURNING notification.id`

// RescheduleNotification releases a valid claim without changing its event or delivery state.
func (s *Store) RescheduleNotification(ctx context.Context, claim core.NotificationClaim, next time.Time) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "reschedule_notification", rescheduleNotification,
		[]any{claim.ID, claim.LockUntil, next},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("reschedule notification: %w", err)
	}
	return nil
}

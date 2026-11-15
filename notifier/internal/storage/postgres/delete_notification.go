package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

const deleteNotification = `
WITH owned AS (
    SELECT id, stop_dt, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND client_id = $2
    FOR UPDATE
), deleted AS (
    DELETE FROM notifier.t_notification AS notification
    USING owned
    WHERE notification.id = owned.id
      AND (owned.stop_dt IS NOT NULL OR owned.lock_until_dt IS NULL OR owned.lock_until_dt <= clock_timestamp())
    RETURNING notification.id
)
SELECT owned.id, deleted.id IS NOT NULL
FROM owned LEFT JOIN deleted USING (id)`

// DeleteNotification removes an owned notification outside an active worker reservation.
func (s *Store) DeleteNotification(ctx context.Context, notificationID uuid.UUID, clientID string) error {
	var id uuid.UUID
	var deleted bool
	err := s.scanRow(ctx, "delete_notification", deleteNotification, []any{notificationID, clientID},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("delete notification: %w", err)
	}
	if !deleted {
		return core.ErrNotificationReserved
	}
	return nil
}

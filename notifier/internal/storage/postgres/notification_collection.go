package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const deleteStoppedNotifications = `
WITH expired AS (
    SELECT id
    FROM notifier.t_notification
    WHERE stop_dt < $1
      AND notification_status IN ('success', 'deadline_exceeded')
    ORDER BY stop_dt, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
), deleted AS (
    DELETE FROM notifier.t_notification AS notification
    USING expired
    WHERE notification.id = expired.id
    RETURNING notification.id
)
SELECT count(*) FROM deleted`

// DeleteStoppedNotifications atomically removes at most limit stopped rows strictly before the cutoff.
func (s *Store) DeleteStoppedNotifications(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 {
		return 0, fmt.Errorf("collection limit must be positive")
	}
	var deleted int64
	err := s.scanRow(ctx, "delete_stopped_notifications", deleteStoppedNotifications, []any{before, limit}, func() slog.Attr {
		return slog.Int64("deleted_count", deleted)
	}, &deleted)
	if err != nil {
		return 0, fmt.Errorf("delete stopped notifications: %w", err)
	}
	return deleted, nil
}

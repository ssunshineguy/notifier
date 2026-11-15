package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

const updateNotification = `
WITH owned AS (
    SELECT id, external_id, deadline_dt, start_dt, notification_status, delivery_status, stop_dt, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND client_id = $2
    FOR UPDATE
), updated AS (
    UPDATE notifier.t_notification AS notification
    SET data = $3
    FROM owned
    WHERE notification.id = owned.id AND owned.stop_dt IS NULL
      AND (owned.lock_until_dt IS NULL OR owned.lock_until_dt <= clock_timestamp())
    RETURNING notification.id
)
SELECT owned.id, owned.external_id, owned.deadline_dt, owned.start_dt,
       owned.notification_status, COALESCE(owned.delivery_status, ''),
       owned.stop_dt IS NOT NULL, updated.id IS NOT NULL
FROM owned LEFT JOIN updated USING (id)`

// UpdateNotification replaces only the conditions and returns the notification as updated.
func (s *Store) UpdateNotification(ctx context.Context, notificationID uuid.UUID, clientID string, conditions []core.Condition) (core.NotificationDetails, error) {
	data, err := json.Marshal(conditionsToJSON(conditions))
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("encode notification params: %w", err)
	}

	var notification core.NotificationDetails
	var stopped, updated bool
	err = s.scanRow(ctx, "update_notification", updateNotification,
		[]any{notificationID, clientID, data},
		func() slog.Attr { return slog.String("notification_id", notification.ID.String()) },
		&notification.ID, &notification.ExternalID, &notification.Deadline, &notification.Start,
		&notification.NotificationStatus, &notification.DeliveryStatus, &stopped, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotificationDetails{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("update notification: %w", err)
	}
	if stopped {
		return core.NotificationDetails{}, core.ErrNotificationStopped
	}
	if !updated {
		return core.NotificationDetails{}, core.ErrNotificationReserved
	}

	notification.Params = conditions
	return notification, nil
}

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/internal/core"
)

const updateNotification = `
UPDATE notifier.t_notification
SET data = $3
WHERE id = $1 AND client_id = $2 AND stop_dt IS NULL
RETURNING id, external_id, deadline_dt, start_dt`

const selectStoppedNotification = `
SELECT id
FROM notifier.t_notification
WHERE id = $1 AND client_id = $2 AND stop_dt IS NOT NULL`

// UpdateNotification replaces only the conditions and returns the notification as updated.
func (s *Store) UpdateNotification(ctx context.Context, notificationID uuid.UUID, clientID string, conditions []core.Condition) (core.NotificationDetails, error) {
	data, err := json.Marshal(conditionsToJSON(conditions))
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("encode notification params: %w", err)
	}

	var notification core.NotificationDetails
	err = s.scanRow(ctx, "update_notification", updateNotification,
		[]any{notificationID, clientID, data},
		func() slog.Attr { return slog.String("notification_id", notification.ID.String()) },
		&notification.ID, &notification.ExternalID, &notification.Deadline, &notification.Start)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotificationDetails{}, s.checkStoppedNotification(ctx, notificationID, clientID)
	}
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("update notification: %w", err)
	}

	notification.Params = conditions
	notification.Status = "active"
	return notification, nil
}

func (s *Store) checkStoppedNotification(ctx context.Context, notificationID uuid.UUID, clientID string) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "select_stopped_notification", selectStoppedNotification,
		[]any{notificationID, clientID},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return core.ErrNotificationNotFound
	case err != nil:
		return fmt.Errorf("check stopped notification: %w", err)
	default:
		return core.ErrNotificationStopped
	}
}

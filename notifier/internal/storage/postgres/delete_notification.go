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
DELETE FROM notifier.t_notification
WHERE id = $1 AND client_id = $2
RETURNING id`

// DeleteNotification removes an owned notification regardless of its lifecycle status.
func (s *Store) DeleteNotification(ctx context.Context, notificationID uuid.UUID, clientID string) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "delete_notification", deleteNotification, []any{notificationID, clientID},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("delete notification: %w", err)
	}
	return nil
}

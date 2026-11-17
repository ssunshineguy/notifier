package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/conditions"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// CreateNotification assigns a UUID v7 or reports an existing or concurrently deleted duplicate.
func (s *Store) CreateNotification(ctx context.Context, n core.Notification) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate notification id: %w", err)
	}

	data, _, err := encodeConditions(n.Params)
	if err != nil {
		return uuid.Nil, err
	}

	var inserted uuid.UUID
	err = s.scanRow(ctx, "insert_notification", insertNotification,
		[]any{id, n.ClientID, n.ExternalID, data, n.Deadline, n.Start, n.NextScheduled},
		func() slog.Attr { return slog.String("notification_id", inserted.String()) }, &inserted)
	if err == nil {
		return inserted, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("insert notification: %w", err)
	}

	var existing uuid.UUID
	err = s.scanRow(ctx, "select_notification_id", selectNotificationID, []any{n.ClientID, n.ExternalID},
		func() slog.Attr { return slog.String("notification_id", existing.String()) }, &existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, core.ErrNotificationDuplicateDisappeared
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read existing notification: %w", err)
	}
	return uuid.Nil, &core.AlreadyExistsError{ID: existing}
}

// GetNotification returns the notification owned by clientID regardless of whether it is stopped.
func (s *Store) GetNotification(ctx context.Context, notificationID uuid.UUID, clientID string) (core.NotificationDetails, error) {
	var notification core.NotificationDetails
	var data []byte
	err := s.scanRow(ctx, "select_notification", selectNotification, []any{notificationID, clientID}, func() slog.Attr {
		return slog.Group("notification",
			slog.String("notification_id", notification.ID.String()),
			slog.String("notification_status", notification.NotificationStatus),
			slog.String("delivery_status", notification.DeliveryStatus))
	},
		&notification.ID,
		&notification.ExternalID,
		&data,
		&notification.Deadline,
		&notification.Start,
		&notification.NotificationStatus,
		&notification.DeliveryStatus,
		&notification.Stop, &notification.DLQAt, &notification.DLQReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotificationDetails{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("query notification: %w", err)
	}

	if notification.NotificationStatus == core.NotificationDLQ {
		return notification, nil
	}

	notification.Params, err = conditions.DecodeStored(data)
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("map notification params: %w", err)
	}
	return notification, nil
}

// FindNotificationID includes stopped notifications while they remain in storage.
func (s *Store) FindNotificationID(ctx context.Context, clientID, externalID string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.scanRow(ctx, "select_notification_id", selectNotificationID, []any{clientID, externalID},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("read existing notification: %w", err)
	}
	return id, true, nil
}

// UpdateNotification replaces only the conditions and returns the notification as updated.
func (s *Store) UpdateNotification(ctx context.Context, notificationID uuid.UUID, clientID string, conditions []core.Condition) (core.NotificationDetails, error) {
	data, normalized, err := encodeConditions(conditions)
	if err != nil {
		return core.NotificationDetails{}, err
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

	notification.Params = normalized
	return notification, nil
}

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

func encodeConditions(params []core.Condition) ([]byte, []core.Condition, error) {
	normalized := conditions.Tree(params).AtSecondPrecision()
	data, err := normalized.MarshalJSON()
	if err != nil {
		return nil, nil, fmt.Errorf("encode notification params: %w", err)
	}
	return data, normalized, nil
}

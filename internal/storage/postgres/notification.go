package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/internal/core"
)

const insertNotification = `
INSERT INTO notifier.t_notification
    (id, client_id, external_id, data, deadline_dt, start_dt, next_scheduled_dt)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT ON CONSTRAINT uq_notification_client_id_external_id DO NOTHING
RETURNING id`

const selectNotification = `
SELECT id, external_id, data, deadline_dt, start_dt, COALESCE(stop_reason, 'active')
FROM notifier.t_notification
WHERE id = $1 AND client_id = $2`

const selectNotificationID = `
SELECT id FROM notifier.t_notification
WHERE client_id = $1 AND external_id = $2`

// CreateNotification assigns a UUID v7 or returns *core.AlreadyExistsError with the existing ID.
func (s *Store) CreateNotification(ctx context.Context, n core.Notification) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate notification id: %w", err)
	}

	data, err := json.Marshal(conditionsToJSON(n.Params))
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode notification params: %w", err)
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
			slog.String("notification_status", notification.Status))
	},
		&notification.ID,
		&notification.ExternalID,
		&data,
		&notification.Deadline,
		&notification.Start,
		&notification.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotificationDetails{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("query notification: %w", err)
	}

	var conditions []conditionJSON
	if err := json.Unmarshal(data, &conditions); err != nil {
		return core.NotificationDetails{}, fmt.Errorf("decode notification params: %w", err)
	}
	notification.Params, err = conditionsFromJSON(conditions)
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

type conditionJSON struct {
	Type       core.ConditionType `json:"type"`
	Operation  string             `json:"operation,omitempty"`
	Value      *time.Time         `json:"value,omitempty"`
	Conditions []conditionJSON    `json:"conditions,omitempty"`
}

func conditionsToJSON(conds []core.Condition) []conditionJSON {
	out := make([]conditionJSON, 0, len(conds))
	for _, c := range conds {
		if c.Type == core.ConditionTime {
			after := c.After
			out = append(out, conditionJSON{Type: c.Type, Operation: "after", Value: &after})
			continue
		}
		out = append(out, conditionJSON{Type: c.Type, Conditions: conditionsToJSON(c.Conditions)})
	}
	return out
}

func conditionsFromJSON(conditions []conditionJSON) ([]core.Condition, error) {
	result := make([]core.Condition, len(conditions))
	for i, condition := range conditions {
		node := &result[i]
		node.Type = condition.Type
		switch condition.Type {
		case core.ConditionTime:
			if condition.Value == nil || condition.Operation != "after" {
				return nil, errors.New("invalid stored time condition")
			}
			node.After = *condition.Value
		case core.ConditionAnd, core.ConditionOr:
			children, err := conditionsFromJSON(condition.Conditions)
			if err != nil {
				return nil, err
			}
			node.Conditions = children
		default:
			return nil, fmt.Errorf("unsupported stored condition type %q", condition.Type)
		}
	}
	return result, nil
}

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

const insertNotification = `
INSERT INTO notifier.t_notification
    (id, client_id, external_id, data, deadline_dt, start_dt, next_scheduled_dt)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT ON CONSTRAINT uq_notification_client_id_external_id DO NOTHING
RETURNING id`

const selectNotification = `
SELECT id, external_id, data, deadline_dt, start_dt, notification_status, COALESCE(delivery_status, '')
FROM notifier.t_notification
WHERE id = $1 AND client_id = $2`

const selectNotificationID = `
SELECT id FROM notifier.t_notification
WHERE client_id = $1 AND external_id = $2`

// CreateNotification assigns a UUID v7 or reports an existing or concurrently deleted duplicate.
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
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotificationDetails{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.NotificationDetails{}, fmt.Errorf("query notification: %w", err)
	}

	var conditions []conditionJSON[*json.RawMessage]
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

type conditionJSON[T any] struct {
	Type       core.ConditionType       `json:"type"`
	Operation  core.ComparisonOperation `json:"operation,omitempty"`
	Value      T                        `json:"value,omitempty"`
	Conditions []conditionJSON[T]       `json:"conditions,omitempty"`
}

func conditionsToJSON(conds []core.Condition) []conditionJSON[any] {
	out := make([]conditionJSON[any], 0, len(conds))
	for _, c := range conds {
		switch c.Type {
		case core.ConditionTime:
			out = append(out, conditionJSON[any]{Type: c.Type, Operation: c.Time.Operation, Value: c.Time.Threshold})
		case core.ConditionPrice:
			out = append(out, conditionJSON[any]{Type: c.Type, Operation: c.Price.Operation, Value: c.Price.Threshold})
		default:
			out = append(out, conditionJSON[any]{Type: c.Type, Conditions: conditionsToJSON(c.Conditions)})
		}
	}
	return out
}

func conditionsFromJSON(conditions []conditionJSON[*json.RawMessage]) ([]core.Condition, error) {
	result := make([]core.Condition, len(conditions))
	for i, condition := range conditions {
		node := &result[i]
		node.Type = condition.Type
		switch condition.Type {
		case core.ConditionTime:
			op := condition.Operation
			if op == "after" {
				// Existing rows can predate the shared comparison operators.
				op = core.GreaterThan
			}
			if condition.Value == nil || !op.IsValid() {
				return nil, errors.New("invalid stored time condition")
			}
			node.Time.Operation = op
			if err := json.Unmarshal(*condition.Value, &node.Time.Threshold); err != nil {
				return nil, fmt.Errorf("invalid stored time condition: %w", err)
			}
		case core.ConditionPrice:
			if !condition.Operation.IsValid() {
				return nil, errors.New("invalid stored price operation")
			}
			if condition.Value == nil {
				return nil, errors.New("missing stored price threshold")
			}
			var threshold int64
			if err := json.Unmarshal(*condition.Value, &threshold); err != nil {
				return nil, fmt.Errorf("invalid stored price threshold: %w", err)
			}
			if threshold < 0 {
				return nil, errors.New("invalid stored price threshold")
			}
			node.Price = core.PriceCondition{Operation: condition.Operation, Threshold: threshold}
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

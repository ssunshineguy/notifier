package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	err = s.scanRow(ctx, insertNotification,
		[]any{id, n.ClientID, n.ExternalID, data, n.Deadline, n.Start, n.NextScheduled}, &inserted)
	if err == nil {
		return inserted, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("insert notification: %w", err)
	}

	var existing uuid.UUID
	err = s.scanRow(ctx, selectNotificationID, []any{n.ClientID, n.ExternalID}, &existing)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read existing notification: %w", err)
	}
	return uuid.Nil, &core.AlreadyExistsError{ID: existing}
}

// FindNotificationID includes stopped notifications while they remain in storage.
func (s *Store) FindNotificationID(ctx context.Context, clientID, externalID string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.scanRow(ctx, selectNotificationID, []any{clientID, externalID}, &id)
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

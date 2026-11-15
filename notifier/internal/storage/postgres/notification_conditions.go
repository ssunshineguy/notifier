package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

const selectClaimedNotificationConditions = `
SELECT client_id, external_id, data, deadline_dt, notification_status, failed_attempt_count
FROM notifier.t_notification
WHERE id = $1 AND stop_dt IS NULL
  AND lock_until_dt = $2 AND lock_until_dt > now()`

// GetClaimedNotificationConditions loads the condition tree while the claim is valid.
func (s *Store) GetClaimedNotificationConditions(ctx context.Context, claim core.NotificationClaim) (core.NotificationConditions, error) {
	var notification core.NotificationConditions
	var data []byte
	err := s.scanRow(ctx, "select_claimed_notification_conditions", selectClaimedNotificationConditions,
		[]any{claim.ID, claim.LockUntil},
		func() slog.Attr { return slog.String("notification_id", claim.ID.String()) },
		&notification.ClientID, &notification.ExternalID, &data, &notification.Deadline,
		&notification.NotificationStatus, &notification.FailedAttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.NotificationConditions{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.NotificationConditions{}, fmt.Errorf("load claimed notification conditions: %w", err)
	}

	var conditions []conditionJSON[*json.RawMessage]
	if err := json.Unmarshal(data, &conditions); err != nil {
		return core.NotificationConditions{}, fmt.Errorf("decode notification params: %w", err)
	}
	notification.Params, err = conditionsFromJSON(conditions)
	if err != nil {
		return core.NotificationConditions{}, fmt.Errorf("map notification params: %w", err)
	}
	return notification, nil
}

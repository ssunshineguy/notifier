package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/conditions"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// ClaimNotifications reserves due rows atomically and returns their IDs and reservation deadlines.
func (s *Store) ClaimNotifications(ctx context.Context, limit int, lockDuration time.Duration) ([]core.NotificationClaim, error) {
	const queryName = "claim_notifications"
	start := time.Now()
	dbCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()

	logger := logging.FromContext(ctx).With("query_name", queryName)
	logger.InfoContext(ctx, "Start SQL "+queryName, "argument_count", 2)
	rows, err := s.pool.Query(dbCtx, claimNotifications, limit, lockDuration.Milliseconds())
	var claims []core.NotificationClaim
	if err == nil {
		claims, err = pgx.CollectRows(rows, pgx.RowToStructByPos[core.NotificationClaim])
	}

	durationMS := time.Since(start).Milliseconds()
	message := fmt.Sprintf("End SQL %s duration %d ms", queryName, durationMS)
	logger = logger.With("duration_ms", durationMS)
	switch {
	case err != nil:
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			logger.InfoContext(ctx, message, "outcome", "error", "sqlstate", postgresError.Code)
		} else {
			logger.InfoContext(ctx, message, "outcome", "error", "error_type", fmt.Sprintf("%T", err))
		}
		return nil, fmt.Errorf("claim notifications: %w", err)
	case len(claims) == 0:
		logger.InfoContext(ctx, message, "outcome", "no_rows", "row_count", 0)
	default:
		logger.InfoContext(ctx, message, "outcome", "ok", "row_count", len(claims))
	}
	return claims, nil
}

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

	if notification.NotificationStatus != core.NotificationActive || !time.Now().Before(notification.Deadline) {
		return notification, nil
	}

	params, err := conditions.DecodeStored(data)
	if err != nil || !params.HasValidSizes() || len(core.ValidateNotificationConditions(params)) != 0 {
		return notification, core.ErrInvalidStoredConditions
	}
	notification.Params = params
	return notification, nil
}

// GetClaimedCallbackTarget reads the callback destination while checking the reservation at delivery start.
func (s *Store) GetClaimedCallbackTarget(ctx context.Context, claim core.NotificationClaim) (core.CallbackTarget, error) {
	var url *string
	var timeoutMS *int32
	err := s.scanRow(ctx, "select_claimed_callback_target", selectClaimedCallbackTarget,
		[]any{claim.ID, claim.LockUntil}, func() slog.Attr { return slog.String("notification_id", claim.ID.String()) }, &url, &timeoutMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.CallbackTarget{}, core.ErrNotificationNotFound
	}
	if err != nil {
		return core.CallbackTarget{}, fmt.Errorf("read claimed callback target: %w", err)
	}
	if url == nil {
		return core.CallbackTarget{}, core.ErrClientNotFound
	}
	target := core.CallbackTarget{URL: *url}
	if timeoutMS != nil {
		timeout := time.Duration(*timeoutMS) * time.Millisecond
		target.Timeout = &timeout
	}
	return target, nil
}

// SaveNotificationResult releases the claim atomically with the result or reports a lost claim.
func (s *Store) SaveNotificationResult(ctx context.Context, claim core.NotificationClaim, result core.NotificationResult) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "update_notification_result", updateNotificationResult,
		[]any{claim.ID, claim.LockUntil, result.NextScheduled, result.FailedAttemptCount, result.NotificationStatus, result.DeliveryStatus},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("save notification result: %w", err)
	}
	return nil
}

// MoveNotificationToDLQ archives the original row and stops its active claim atomically.
func (s *Store) MoveNotificationToDLQ(ctx context.Context, claim core.NotificationClaim) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "move_notification_to_dlq", moveNotificationToDLQ,
		[]any{claim.ID, claim.LockUntil, core.DLQInvalidStoredConditions},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("move notification to dlq: %w", err)
	}
	return nil
}

// RescheduleNotification releases a valid claim without changing its event or delivery state.
func (s *Store) RescheduleNotification(ctx context.Context, claim core.NotificationClaim, next time.Time) error {
	var id uuid.UUID
	err := s.scanRow(ctx, "reschedule_notification", rescheduleNotification,
		[]any{claim.ID, claim.LockUntil, next},
		func() slog.Attr { return slog.String("notification_id", id.String()) }, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotificationNotFound
	}
	if err != nil {
		return fmt.Errorf("reschedule notification: %w", err)
	}
	return nil
}

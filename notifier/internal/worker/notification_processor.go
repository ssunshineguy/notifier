package worker

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// ProcessingStore reads the data for one active claim and records its outcome.
type ProcessingStore interface {
	GetClaimedNotificationConditions(context.Context, core.NotificationClaim) (core.NotificationConditions, error)
	GetClaimedCallbackTarget(context.Context, core.NotificationClaim) (core.CallbackTarget, error)
	SaveNotificationResult(context.Context, core.NotificationClaim, core.NotificationResult) error
}

// Callback sends an event and returns an error if the recipient does not confirm delivery.
type Callback interface {
	Send(ctx context.Context, endpoint, requestID string, event core.NotificationEvent) (int, error)
}

// ProcessingSettings controls when to recheck conditions and how much time to reserve for saving a result.
type ProcessingSettings struct {
	ScheduledDelay     time.Duration
	ResultWriteTimeout time.Duration
}

// ProcessNotification checks or retries one claim and saves its next state.
func ProcessNotification(ctx context.Context, claim core.NotificationClaim, store ProcessingStore, source InformationService, callback Callback, settings ProcessingSettings) {
	requestID := uuid.NewString()
	logger := logging.FromContext(ctx).With("notification_id", claim.ID.String(), "request_id", requestID)
	ctx = logging.WithLogger(ctx, logger)

	notification, err := store.GetClaimedNotificationConditions(ctx, claim)
	if errors.Is(err, core.ErrNotificationNotFound) {
		logger.DebugContext(ctx, "Notification condition check", "outcome", "claim_unavailable")
		return
	}
	if err != nil {
		logger.WarnContext(ctx, "Notification condition check", "outcome", "load_failed")
		return
	}

	status := notification.NotificationStatus
	if status == core.NotificationActive && time.Now().Before(notification.Deadline) {
		met := checkConditions(ctx, notification, source, requestID)
		checkedAt := time.Now()
		if checkedAt.Before(notification.Deadline) {
			if !met {
				next := checkedAt.Add(settings.ScheduledDelay)
				if next.After(notification.Deadline) {
					next = notification.Deadline
				}
				saveResult(ctx, store, claim, core.NotificationResult{
					NextScheduled: &next, FailedAttemptCount: notification.FailedAttemptCount, NotificationStatus: core.NotificationActive,
				})
				return
			}
			status = core.EventSuccess
		}
	}
	if status == core.NotificationActive {
		status = core.EventDeadlineExceeded
	}

	target, err := store.GetClaimedCallbackTarget(ctx, claim)
	if err != nil {
		switch {
		case errors.Is(err, core.ErrNotificationNotFound):
			logger.DebugContext(ctx, "Notification callback", "outcome", "claim_unavailable")
		case errors.Is(err, core.ErrClientNotFound):
			logger.WarnContext(ctx, "Notification callback", "outcome", "client_unavailable")
		default:
			logger.WarnContext(ctx, "Notification callback", "outcome", "target_lookup_failed")
		}
		return
	}
	event := core.NotificationEvent{NotificationID: claim.ID, ExternalID: notification.ExternalID, Status: status}
	if result, attempted := deliver(ctx, callback, target, requestID, event, notification.FailedAttemptCount, settings.ResultWriteTimeout); attempted {
		saveResult(ctx, store, claim, result)
	}
}

func deliver(ctx context.Context, callback Callback, target core.CallbackTarget, requestID string, event core.NotificationEvent, failedAttempts int, writeTimeout time.Duration) (core.NotificationResult, bool) {
	timeout := time.Second
	if target.Timeout != nil {
		timeout = *target.Timeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		writeBudget := min(writeTimeout, remaining/2)
		timeout = min(timeout, remaining-writeBudget)
	}
	callbackCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if callbackCtx.Err() != nil {
		return core.NotificationResult{}, false
	}

	startedAt := time.Now()
	event.SentAt = startedAt.UTC()
	httpStatus, err := callback.Send(callbackCtx, target.URL, requestID, event)
	finishedAt := time.Now()
	outcome := "delivered"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		outcome = "timeout"
	case httpStatus != 0 && err != nil:
		outcome = "rejected"
	case err != nil:
		outcome = "transport_error"
	}
	logging.FromContext(ctx).InfoContext(ctx, "Notification callback",
		"outcome", outcome, "http_status", httpStatus, "duration_ms", finishedAt.Sub(startedAt).Milliseconds())
	return core.AfterDelivery(event.Status, failedAttempts, err == nil, finishedAt), true
}

func saveResult(ctx context.Context, store ProcessingStore, claim core.NotificationClaim, result core.NotificationResult) {
	logger := logging.FromContext(ctx)
	err := store.SaveNotificationResult(ctx, claim, result)
	switch {
	case errors.Is(err, core.ErrNotificationNotFound):
		logger.DebugContext(ctx, "Save notification result", "outcome", "claim_unavailable")
	case err != nil:
		logger.WarnContext(ctx, "Save notification result", "outcome", "write_failed")
	}
}

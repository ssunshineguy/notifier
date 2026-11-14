package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// ConditionStore loads the conditions for a claim that the worker still owns.
type ConditionStore interface {
	GetClaimedNotificationConditions(context.Context, core.NotificationClaim) (core.NotificationConditions, error)
}

// InformationService provides current values for a client's conditions.
type InformationService interface {
	GetTime(ctx context.Context, clientID, requestID string) (time.Time, error)
	GetPrice(ctx context.Context, clientID, requestID string) (int64, error)
}

// ProcessConditions checks a claimed notification and logs the outcome.
func ProcessConditions(ctx context.Context, claim core.NotificationClaim, store ConditionStore, source InformationService) {
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

	needsTime, needsPrice := requiredValues(notification.Params)
	var requests sync.WaitGroup
	var values core.ConditionValues
	if needsTime {
		requests.Go(func() {
			values.Time, values.TimeErr = source.GetTime(ctx, notification.ClientID, requestID)
		})
	}
	if needsPrice {
		requests.Go(func() {
			values.Price, values.PriceErr = source.GetPrice(ctx, notification.ClientID, requestID)
		})
	}
	requests.Wait()

	met, err := core.ConditionsMet(notification.Params, values)
	if err != nil {
		outcome := "price_unavailable"
		if values.TimeErr != nil {
			outcome = "time_unavailable"
		}
		logger.WarnContext(ctx, "Notification condition check", "outcome", outcome)
		return
	}
	logger.InfoContext(ctx, "Notification condition check", "met", met)
}

func requiredValues(conditions []core.Condition) (needsTime, needsPrice bool) {
	for _, condition := range conditions {
		switch condition.Type {
		case core.ConditionTime:
			needsTime = true
		case core.ConditionPrice:
			needsPrice = true
		case core.ConditionAnd, core.ConditionOr:
			childTime, childPrice := requiredValues(condition.Conditions)
			needsTime = needsTime || childTime
			needsPrice = needsPrice || childPrice
		}
	}
	return needsTime, needsPrice
}

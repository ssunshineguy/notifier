package worker

import (
	"context"
	"sync"
	"time"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// InformationService provides current values for a client's conditions.
type InformationService interface {
	GetTime(ctx context.Context, clientID, requestID string) (time.Time, error)
	GetPrice(ctx context.Context, clientID, requestID string) (int64, error)
}

func checkConditions(ctx context.Context, notification core.NotificationConditions, source InformationService, requestID string) bool {
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

	logger := logging.FromContext(ctx)
	met, err := core.ConditionsMet(notification.Params, values)
	if err != nil {
		outcome := "price_unavailable"
		if values.TimeErr != nil {
			outcome = "time_unavailable"
		}
		logger.WarnContext(ctx, "Notification condition check", "outcome", outcome)
		return false
	}
	logger.InfoContext(ctx, "Notification condition check", "met", met)
	return met
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

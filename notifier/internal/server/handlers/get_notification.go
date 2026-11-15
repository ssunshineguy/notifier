package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

// GetNotification reads the notification scoped to the client resolved for the request.
func (h *Handler) GetNotification(ctx context.Context, params codegen.GetNotificationParams) (codegen.GetNotificationRes, error) {
	requestID := codegen.NewOptString(params.XRequestID)
	client, ok := middleware.ClientFromContext(ctx)
	if !ok {
		return internalErrorResponse(requestID), nil
	}

	notification, err := h.store.GetNotification(ctx, uuid.UUID(params.NotificationID), client.ID)
	if errors.Is(err, core.ErrNotificationNotFound) {
		return notificationNotFoundResponse(requestID), nil
	}
	if err != nil {
		return internalErrorResponse(requestID), nil
	}

	if notification.NotificationStatus == core.NotificationDLQ {
		if notification.Stop == nil || notification.DLQAt == nil || notification.DLQReason != core.DLQInvalidStoredConditions {
			return internalErrorResponse(requestID), nil
		}
		return &codegen.NotificationReadHeaders{
			XRequestID: requestID,
			Response: codegen.NewDLQNotificationNotificationRead(codegen.DLQNotification{
				NotificationID:     codegen.NotificationId(notification.ID),
				ExternalID:         codegen.ExternalId(notification.ExternalID),
				NotificationStatus: codegen.DLQNotificationNotificationStatusDlq,
				DeadlineDt:         notification.Deadline, StartDt: notification.Start,
				StopDt: *notification.Stop, DlqDt: *notification.DLQAt,
				DlqReason: codegen.DLQNotificationDlqReasonInvalidStoredConditions,
			}),
		}, nil
	}

	response := notificationResponse(requestID, notification)
	if response == nil {
		return internalErrorResponse(requestID), nil
	}
	return &codegen.NotificationReadHeaders{
		XRequestID: requestID,
		Response:   codegen.NewNotificationNotificationRead(response.Response),
	}, nil
}

func notificationNotFoundResponse(requestID codegen.OptString) *codegen.NotFoundHeaders {
	return &codegen.NotFoundHeaders{
		XRequestID: requestID,
		Response: codegen.Problem{
			Type:      "/problems/not-found",
			Title:     "Notification not found",
			Status:    http.StatusNotFound,
			RequestID: requestID,
		},
	}
}

func notificationResponse(requestID codegen.OptString, notification core.NotificationDetails) *codegen.NotificationHeaders {
	conditions, ok := toAPIConditions(notification.Params)
	if !ok {
		return nil
	}
	var deliveryStatus codegen.OptDeliveryStatus
	if notification.DeliveryStatus != "" {
		deliveryStatus.SetTo(codegen.DeliveryStatus(notification.DeliveryStatus))
	}
	return &codegen.NotificationHeaders{
		XRequestID: requestID,
		Response: codegen.Notification{
			NotificationID:     codegen.NotificationId(notification.ID),
			ExternalID:         codegen.ExternalId(notification.ExternalID),
			NotificationStatus: codegen.NewOptOrdinaryNotificationStatus(codegen.OrdinaryNotificationStatus(notification.NotificationStatus)),
			DeliveryStatus:     deliveryStatus,
			NotificationParams: conditions,
			DeadlineDt:         notification.Deadline,
			StartDt:            notification.Start,
		},
	}
}

func toAPIConditions(conditions []core.Condition) ([]codegen.Condition, bool) {
	result := make([]codegen.Condition, len(conditions))
	for i, condition := range conditions {
		if condition.Type == core.ConditionTime {
			result[i] = codegen.NewTimeConditionCondition(codegen.TimeCondition{
				Type:      codegen.TimeConditionTypeTime,
				Operation: codegen.ComparisonOperation(condition.Time.Operation),
				Value:     condition.Time.Threshold,
			})
			continue
		}
		if condition.Type == core.ConditionPrice {
			result[i] = codegen.NewPriceConditionCondition(codegen.PriceCondition{
				Type:      codegen.PriceConditionTypePrice,
				Operation: codegen.ComparisonOperation(condition.Price.Operation),
				Value:     condition.Price.Threshold,
			})
			continue
		}

		children, ok := toAPIConditions(condition.Conditions)
		if !ok {
			return nil, false
		}
		switch condition.Type {
		case core.ConditionAnd:
			result[i] = codegen.NewAndConditionCondition(codegen.AndCondition{
				Type:       codegen.AndConditionTypeAnd,
				Conditions: children,
			})
		case core.ConditionOr:
			result[i] = codegen.NewOrConditionCondition(codegen.OrCondition{
				Type:       codegen.OrConditionTypeOr,
				Conditions: children,
			})
		default:
			return nil, false
		}
	}
	return result, true
}

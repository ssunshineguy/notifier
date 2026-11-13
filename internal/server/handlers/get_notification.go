package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/internal/server/middleware"
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

	response := notificationResponse(requestID, notification)
	if response == nil {
		return internalErrorResponse(requestID), nil
	}
	return response, nil
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
	return &codegen.NotificationHeaders{
		XRequestID: requestID,
		Response: codegen.Notification{
			NotificationID:     codegen.NotificationId(notification.ID),
			ExternalID:         codegen.ExternalId(notification.ExternalID),
			NotificationStatus: codegen.NewOptNotificationStatus(codegen.NotificationStatus(notification.Status)),
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
				Operation: codegen.TimeConditionOperationAfter,
				Value:     condition.After,
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

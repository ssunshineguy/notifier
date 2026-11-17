package handlers

import (
	"context"
	"errors"

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

package handlers

import (
	"net/http"

	"github.com/ssunshineguy/notifier/notifier/internal/conditions"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
)

func internalErrorResponse(requestID codegen.OptString) *codegen.InternalErrorHeaders {
	return &codegen.InternalErrorHeaders{
		XRequestID: requestID,
		Response: codegen.Problem{
			Type:      "/problems/internal-error",
			Title:     "Internal server error",
			Status:    http.StatusInternalServerError,
			RequestID: requestID,
		},
	}
}

func validationResponse(requestID codegen.OptString, validationFields []core.FieldError) *codegen.BadRequestHeaders {
	fields := make([]codegen.FieldError, len(validationFields))
	for i, field := range validationFields {
		fields[i] = codegen.FieldError{Field: field.Field, Message: field.Message}
	}

	return &codegen.BadRequestHeaders{
		XRequestID: requestID,
		Response: codegen.Problem{
			Type:      "/problems/validation-error",
			Title:     "Request validation failed",
			Status:    http.StatusBadRequest,
			RequestID: requestID,
			Errors:    fields,
		},
	}
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
	params := conditions.Tree(notification.Params)
	if !params.CanEncode() {
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
			NotificationParams: codegen.NotificationParams(params),
			DeadlineDt:         notification.Deadline,
			StartDt:            notification.Start,
		},
	}
}

func notificationInProgressResponse(requestID codegen.OptString) *codegen.NotificationInProgressHeaders {
	return &codegen.NotificationInProgressHeaders{
		XRequestID: requestID,
		Response: codegen.Problem{
			Type:      "/problems/notification-in-progress",
			Title:     "Notification is being processed",
			Status:    http.StatusConflict,
			RequestID: requestID,
		},
	}
}

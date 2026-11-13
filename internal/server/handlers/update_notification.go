package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/internal/server/middleware"
)

// UpdateNotification replaces the conditions of an active notification owned by the client.
func (h *Handler) UpdateNotification(ctx context.Context, req *codegen.NotificationUpdate, params codegen.UpdateNotificationParams) (codegen.UpdateNotificationRes, error) {
	requestID := codegen.NewOptString(params.XRequestID)
	client, ok := middleware.ClientFromContext(ctx)
	if !ok {
		return internalErrorResponse(requestID), nil
	}

	conditions, err := toCoreConditions(req.NotificationParams)
	if err != nil {
		return internalErrorResponse(requestID), nil
	}
	logger := logging.FromContext(ctx)
	logger.InfoContext(ctx, "API Request Parameters", "top_level_condition_count", len(conditions))
	if fields := core.ValidateNotificationConditions(conditions); len(fields) != 0 {
		logger.InfoContext(ctx, "Notification Condition Checks", "outcome", "failed", "invalid_field_count", len(fields))
		return validationResponse(requestID, fields), nil
	}
	logger.InfoContext(ctx, "Notification Condition Checks", "outcome", "passed")

	notification, err := h.store.UpdateNotification(ctx, uuid.UUID(params.NotificationID), client.ID, conditions)
	switch {
	case errors.Is(err, core.ErrNotificationNotFound):
		return notificationNotFoundResponse(requestID), nil
	case errors.Is(err, core.ErrNotificationStopped):
		return &codegen.BadRequestHeaders{
			XRequestID: requestID,
			Response: codegen.Problem{
				Type:      "/problems/incorrect-notification-status",
				Title:     "Incorrect notification status",
				Status:    http.StatusBadRequest,
				RequestID: requestID,
			},
		}, nil
	case err != nil:
		return internalErrorResponse(requestID), nil
	}

	response := notificationResponse(requestID, notification)
	if response == nil {
		return internalErrorResponse(requestID), nil
	}
	return response, nil
}

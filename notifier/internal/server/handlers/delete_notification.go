package handlers

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

// DeleteNotification removes a notification owned by the client.
func (h *Handler) DeleteNotification(ctx context.Context, params codegen.DeleteNotificationParams) (codegen.DeleteNotificationRes, error) {
	requestID := codegen.NewOptString(params.XRequestID)
	client, ok := middleware.ClientFromContext(ctx)
	if !ok {
		return internalErrorResponse(requestID), nil
	}

	err := h.store.DeleteNotification(ctx, uuid.UUID(params.NotificationID), client.ID)
	switch {
	case errors.Is(err, core.ErrNotificationNotFound):
		return notificationNotFoundResponse(requestID), nil
	case errors.Is(err, core.ErrNotificationReserved):
		return notificationInProgressResponse(requestID), nil
	case err != nil:
		return internalErrorResponse(requestID), nil
	default:
		return &codegen.DeleteNotificationOK{XRequestID: requestID}, nil
	}
}

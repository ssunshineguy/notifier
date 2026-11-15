// Package handlers connects the emulator's OpenAPI operations to its override store.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/emulator/internal/server/codegen"
)

// ReceiveNotificationEvent returns the response configured for the notification.
func (h *Handler) ReceiveNotificationEvent(ctx context.Context, req *codegen.NotificationEvent) (codegen.ReceiveNotificationEventRes, error) {
	status, delay, found := h.callbacks.CaptureCallback(
		uuid.UUID(req.NotificationID), string(req.ExternalID), string(req.NotificationStatus), req.SentDt,
	)
	if !found {
		return &codegen.ReceiveNotificationEventOK{}, nil
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			status = http.StatusServiceUnavailable
		case <-timer.C:
		}
	}

	problem := codegen.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status}
	switch status {
	case http.StatusOK:
		return &codegen.ReceiveNotificationEventOK{}, nil
	case http.StatusCreated:
		return &codegen.ReceiveNotificationEventCreated{}, nil
	case http.StatusBadRequest:
		return (*codegen.ReceiveNotificationEventBadRequest)(&problem), nil
	case http.StatusNotFound:
		return (*codegen.ReceiveNotificationEventNotFound)(&problem), nil
	case http.StatusInternalServerError:
		return (*codegen.ReceiveNotificationEventInternalServerError)(&problem), nil
	case http.StatusServiceUnavailable:
		return (*codegen.ReceiveNotificationEventServiceUnavailable)(&problem), nil
	default:
		return nil, errors.New("unexpected callback status")
	}
}

// GetReceivedCallback returns the event captured for a configured callback override.
func (h *Handler) GetReceivedCallback(_ context.Context, params codegen.GetReceivedCallbackParams) (codegen.GetReceivedCallbackRes, error) {
	requestID := codegen.NewOptString(params.XRequestID)
	externalID, status, sentAt, found := h.callbacks.ReceivedCallback(uuid.UUID(params.NotificationID))
	if !found {
		return &codegen.ReceivedCallbackNotFoundHeaders{
			XRequestID: requestID,
			Response: codegen.Problem{
				Type: "/problems/not-found", Title: "Callback event not found",
				Status: http.StatusNotFound, RequestID: requestID,
			},
		}, nil
	}
	return &codegen.NotificationEventHeaders{
		XRequestID: requestID,
		Response: codegen.NotificationEvent{
			NotificationID:     params.NotificationID,
			ExternalID:         codegen.ExternalId(externalID),
			NotificationStatus: codegen.EventStatus(status),
			SentDt:             sentAt,
		},
	}, nil
}

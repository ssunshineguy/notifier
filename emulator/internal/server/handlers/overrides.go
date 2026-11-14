package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/emulator/internal/server/codegen"
)

// SetClientOverride stores the selected Information Service value.
func (h *Handler) SetClientOverride(_ context.Context, req codegen.ClientOverride, params codegen.SetClientOverrideParams) (codegen.SetClientOverrideRes, error) {
	switch req.Type {
	case codegen.ClientTimeOverrideClientOverride:
		value := req.ClientTimeOverride.Value.UTC()
		if value.Year() < 0 || value.Year() > 9999 {
			return &codegen.BadOverrideRequestHeaders{
				XRequestID: codegen.NewOptString(params.XRequestID),
				Response: codegen.Problem{
					Type:      "/problems/validation-error",
					Title:     "Request validation failed",
					Status:    http.StatusBadRequest,
					Detail:    codegen.NewOptString("The UTC year must be between 0000 and 9999."),
					RequestID: codegen.NewOptString(params.XRequestID),
				},
			}, nil
		}
		h.store.SetTime(params.ClientID, value)
	case codegen.ClientPriceOverrideClientOverride:
		h.store.SetPrice(params.ClientID, req.ClientPriceOverride.Value)
	default:
		return nil, errors.New("unexpected client override type")
	}
	return &codegen.OverrideApplied{XRequestID: codegen.NewOptString(params.XRequestID)}, nil
}

// ResetClientOverrides drops the client's Information Service values.
func (h *Handler) ResetClientOverrides(_ context.Context, params codegen.ResetClientOverridesParams) (codegen.ResetClientOverridesRes, error) {
	h.store.ResetClient(params.ClientID)
	return &codegen.OverrideApplied{XRequestID: codegen.NewOptString(params.XRequestID)}, nil
}

// SetCallbackOverride stores the status and delay for a notification.
func (h *Handler) SetCallbackOverride(_ context.Context, req *codegen.CallbackOverride, params codegen.SetCallbackOverrideParams) (codegen.SetCallbackOverrideRes, error) {
	delay := time.Duration(req.ResponseTimeout) * time.Millisecond
	h.store.SetCallback(uuid.UUID(params.NotificationID), int(req.StatusCode), delay)
	return &codegen.OverrideApplied{XRequestID: codegen.NewOptString(params.XRequestID)}, nil
}

// ResetCallbackOverride drops the response override for a notification.
func (h *Handler) ResetCallbackOverride(_ context.Context, params codegen.ResetCallbackOverrideParams) (codegen.ResetCallbackOverrideRes, error) {
	h.store.ResetCallback(uuid.UUID(params.NotificationID))
	return &codegen.OverrideApplied{XRequestID: codegen.NewOptString(params.XRequestID)}, nil
}

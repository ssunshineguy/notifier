package handlers

import (
	"context"
	"time"

	"github.com/ssunshineguy/notifier/emulator/internal/server/codegen"
)

// GetTime returns the emulated clock value.
func (h *Handler) GetTime(_ context.Context, params codegen.GetTimeParams) (codegen.GetTimeRes, error) {
	var value time.Time
	if preset := h.clients.Time(params.XClientID); preset != nil {
		value = *preset
	} else {
		value = time.Now().UTC()
	}
	return &codegen.TimeHeaders{
		XRequestID: codegen.NewOptString(params.XRequestID),
		Response:   codegen.Time{Time: value},
	}, nil
}

// GetPrice returns the emulated price.
func (h *Handler) GetPrice(_ context.Context, params codegen.GetPriceParams) (codegen.GetPriceRes, error) {
	return &codegen.PriceHeaders{
		XRequestID: codegen.NewOptString(params.XRequestID),
		Response:   codegen.Price{Price: h.clients.Price(params.XClientID)},
	}, nil
}

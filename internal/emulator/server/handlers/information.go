package handlers

import (
	"context"
	"time"

	"github.com/ssunshineguy/notifier/internal/emulator/server/codegen"
)

// GetTime returns the emulated clock value.
func (h *Handler) GetTime(_ context.Context, params codegen.GetTimeParams) (codegen.GetTimeRes, error) {
	value, found := h.store.Time(params.XClientID)
	if !found {
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
		Response:   codegen.Price{Price: h.store.Price(params.XClientID)},
	}, nil
}

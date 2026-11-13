package handlers

import (
	"time"

	"github.com/google/uuid"
)

// Store is the emulator's override storage.
type Store interface {
	Time(clientID string) (time.Time, bool)
	Price(clientID string) int64
	SetTime(clientID string, value time.Time)
	SetPrice(clientID string, value int64)
	ResetClient(clientID string)
	Callback(notificationID uuid.UUID) (int, time.Duration, bool)
	SetCallback(notificationID uuid.UUID, status int, delay time.Duration)
	ResetCallback(notificationID uuid.UUID)
}

// Handler implements the emulator's HTTP operations.
type Handler struct {
	store Store
}

// New creates an emulator handler.
func New(store Store) *Handler {
	return &Handler{store: store}
}

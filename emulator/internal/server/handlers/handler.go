package handlers

import (
	"time"

	"github.com/google/uuid"
)

// ClientStore provides the presets used by the Information Service endpoints.
type ClientStore interface {
	Time(clientID string) *time.Time
	Price(clientID string) int64
	SetTime(clientID string, value time.Time)
	SetPrice(clientID string, value int64)
	ResetClient(clientID string)
}

// CallbackStore provides the response presets used by the callback endpoint.
type CallbackStore interface {
	Callback(notificationID uuid.UUID) (int, time.Duration, bool)
	SetCallback(notificationID uuid.UUID, status int, delay time.Duration)
	ResetCallback(notificationID uuid.UUID)
}

// Handler implements the emulator's HTTP operations.
type Handler struct {
	clients   ClientStore
	callbacks CallbackStore
}

// New creates an emulator handler.
func New(clients ClientStore, callbacks CallbackStore) *Handler {
	return &Handler{clients: clients, callbacks: callbacks}
}

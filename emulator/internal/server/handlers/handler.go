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

// CallbackStore provides response presets and callback capture for their notifications.
type CallbackStore interface {
	CaptureCallback(notificationID uuid.UUID, externalID, status string, sentAt time.Time) (int, time.Duration, bool)
	ReceivedCallback(notificationID uuid.UUID) (externalID, status string, sentAt time.Time, found bool)
	SetCallback(notificationID uuid.UUID, status int, delay time.Duration, capture bool)
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

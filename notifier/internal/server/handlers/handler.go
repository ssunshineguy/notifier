// Package handlers adapts Notifier's HTTP operations to its domain logic.
package handlers

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
)

// Store reads and writes notifications for the API.
type Store interface {
	CreateNotification(ctx context.Context, notification core.Notification) (uuid.UUID, error)
	FindNotificationID(ctx context.Context, clientID, externalID string) (uuid.UUID, bool, error)
	GetNotification(ctx context.Context, notificationID uuid.UUID, clientID string) (core.NotificationDetails, error)
	UpdateNotification(ctx context.Context, notificationID uuid.UUID, clientID string, conditions []core.Condition) (core.NotificationDetails, error)
	DeleteNotification(ctx context.Context, notificationID uuid.UUID, clientID string) error
}

// Handler implements the Notifier operations defined in OpenAPI.
type Handler struct {
	codegen.UnimplementedHandler
	store         Store
	scheduledTime time.Duration
}

var _ codegen.Handler = (*Handler)(nil)

// NewHandler supplies the store and the delay before the first notification check.
func NewHandler(store Store, scheduledTime time.Duration) *Handler {
	return &Handler{store: store, scheduledTime: scheduledTime}
}

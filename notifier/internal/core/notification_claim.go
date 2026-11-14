package core

import (
	"time"

	"github.com/google/uuid"
)

// NotificationClaim identifies a notification and its reserved processing window.
type NotificationClaim struct {
	ID        uuid.UUID
	LockUntil time.Time
}

// NotificationConditions holds the client and condition tree for an active claim.
type NotificationConditions struct {
	ClientID string
	Params   []Condition
}

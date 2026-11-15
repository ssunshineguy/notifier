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

// NotificationConditions holds the inputs needed to check and deliver an active claim.
type NotificationConditions struct {
	ClientID           string
	ExternalID         string
	Params             []Condition
	Deadline           time.Time
	NotificationStatus string
	FailedAttemptCount int
}

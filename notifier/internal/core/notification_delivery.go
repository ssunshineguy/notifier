package core

import (
	"time"

	"github.com/google/uuid"
)

const (
	// NotificationActive means the conditions have yet to trigger an event.
	NotificationActive = "active"
	// NotificationDLQ means required saved conditions cannot be processed.
	NotificationDLQ = "dlq"
	// DLQInvalidStoredConditions is a safe reason code without saved condition values.
	DLQInvalidStoredConditions = "invalid_stored_conditions"
	// EventSuccess means the worker observed that the conditions were met.
	EventSuccess = "success"
	// EventDeadlineExceeded means the deadline arrived before a condition match was observed.
	EventDeadlineExceeded = "deadline_exceeded"

	// DeliveryTryingToDeliver means a further callback attempt is scheduled.
	DeliveryTryingToDeliver = "trying_to_deliver"
	// DeliveryDelivered means the recipient confirmed delivery.
	DeliveryDelivered = "delivered"
	// DeliveryFailed means the retry budget is exhausted.
	DeliveryFailed = "delivery_failed"

	attemptsPerStep = 3
	maxAttempts     = (1 + 13) * attemptsPerStep
)

// NotificationEvent identifies a callback attempt and its outcome from the worker's perspective.
type NotificationEvent struct {
	NotificationID uuid.UUID
	ExternalID     string
	Status         string
	SentAt         time.Time
}

// CallbackTarget holds the destination and optional per-client response budget.
type CallbackTarget struct {
	URL     string
	Timeout *time.Duration
}

// NotificationResult records the next check or the final state to save before releasing a claim.
type NotificationResult struct {
	NextScheduled      *time.Time
	FailedAttemptCount int
	NotificationStatus string
	DeliveryStatus     string
}

// AfterDelivery schedules another attempt or stops the notification based on the callback result.
func AfterDelivery(eventStatus string, failedAttempts int, delivered bool, finishedAt time.Time) NotificationResult {
	result := NotificationResult{NotificationStatus: eventStatus, FailedAttemptCount: failedAttempts}
	if delivered {
		result.DeliveryStatus = DeliveryDelivered
		return result
	}

	result.FailedAttemptCount = min(failedAttempts+1, maxAttempts)
	if result.FailedAttemptCount == maxAttempts {
		result.DeliveryStatus = DeliveryFailed
		return result
	}

	result.DeliveryStatus = DeliveryTryingToDeliver
	next := finishedAt
	if result.FailedAttemptCount%attemptsPerStep == 0 {
		step := result.FailedAttemptCount/attemptsPerStep - 1
		next = next.Add(4 * time.Second << step)
	}
	result.NextScheduled = &next
	return result
}

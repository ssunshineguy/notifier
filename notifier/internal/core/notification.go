// Package core holds the domain rules of Notifier: no I/O, no transport or storage types.
package core

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// CreateRequest is a validated-by-schema request to create a notification.
type CreateRequest struct {
	ExternalID string
	Params     []Condition
	Deadline   *time.Time
}

// Notification is a new notification ready to be stored.
type Notification struct {
	ClientID      string
	ExternalID    string
	Params        []Condition
	Deadline      time.Time
	Start         time.Time
	NextScheduled time.Time
}

// NotificationDetails holds a stored notification's parameters and lifecycle status.
type NotificationDetails struct {
	ID         uuid.UUID
	ExternalID string
	Params     []Condition
	Deadline   time.Time
	Start      time.Time
	Status     string
}

// ErrNotificationNotFound means no notification matched the lookup.
var ErrNotificationNotFound = errors.New("notification not found")

// ErrNotificationStopped means the notification no longer accepts condition changes.
var ErrNotificationStopped = errors.New("notification stopped")

// AlreadyExistsError reports that the client already has a notification with the same external ID.
type AlreadyExistsError struct {
	ID uuid.UUID
}

func (e *AlreadyExistsError) Error() string {
	return "notification already exists: " + e.ID.String()
}

// NewNotification validates the request against the rules the API schema cannot express and
// builds the notification that starts at now and is first checked after scheduledTime.
func NewNotification(client Client, req CreateRequest, now time.Time, scheduledTime time.Duration) (Notification, error) {
	fieldErrs := ValidateNotificationConditions(req.Params)

	maxDeadline := now.Add(client.MaxLifetime)
	var deadline time.Time
	if req.Deadline != nil {
		deadline = *req.Deadline
		fieldErrs = append(fieldErrs, checkDeadline(deadline, now, maxDeadline, req.Params)...)
	} else {
		// Emulated conditions may outlive the default deadline, so skip the reachability check.
		deadline = maxDeadline
	}

	if len(fieldErrs) > 0 {
		return Notification{}, &ValidationError{Fields: fieldErrs}
	}

	return Notification{
		ClientID:      client.ID,
		ExternalID:    req.ExternalID,
		Params:        req.Params,
		Deadline:      deadline,
		Start:         now,
		NextScheduled: now.Add(scheduledTime),
	}, nil
}

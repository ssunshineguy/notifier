package memory

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// CallbackCache stores response presets and their latest received events.
type CallbackCache struct {
	mu        sync.RWMutex
	callbacks map[uuid.UUID]callbackOverride
}

type callbackOverride struct {
	status   int
	delay    time.Duration
	capture  bool
	received *callbackEvent
}

type callbackEvent struct {
	externalID string
	status     string
	sentAt     time.Time
}

// NewCallbackCache creates an empty cache keyed by notification ID.
func NewCallbackCache() *CallbackCache {
	return &CallbackCache{callbacks: make(map[uuid.UUID]callbackOverride)}
}

// CaptureCallback records the latest event only for overrides that enable capture.
func (c *CallbackCache) CaptureCallback(notificationID uuid.UUID, externalID, status string, sentAt time.Time) (int, time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, found := c.callbacks[notificationID]
	if found && value.capture {
		value.received = &callbackEvent{externalID: externalID, status: status, sentAt: sentAt}
		c.callbacks[notificationID] = value
	}
	return value.status, value.delay, found
}

// ReceivedCallback returns the latest event captured for the response override.
func (c *CallbackCache) ReceivedCallback(notificationID uuid.UUID) (externalID, status string, sentAt time.Time, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	event := c.callbacks[notificationID].received
	if event == nil {
		return "", "", time.Time{}, false
	}
	return event.externalID, event.status, event.sentAt, true
}

// SetCallback replaces the response override for the notification.
func (c *CallbackCache) SetCallback(notificationID uuid.UUID, status int, delay time.Duration, capture bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callbacks[notificationID] = callbackOverride{status: status, delay: delay, capture: capture}
}

// ResetCallback clears the response override for the notification.
func (c *CallbackCache) ResetCallback(notificationID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.callbacks, notificationID)
}

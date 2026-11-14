package memory

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// CallbackCache stores response presets for individual notifications.
type CallbackCache struct {
	mu        sync.RWMutex
	callbacks map[uuid.UUID]callbackOverride
}

type callbackOverride struct {
	status int
	delay  time.Duration
}

// NewCallbackCache creates an empty cache keyed by notification ID.
func NewCallbackCache() *CallbackCache {
	return &CallbackCache{callbacks: make(map[uuid.UUID]callbackOverride)}
}

// Callback returns the configured status and delay, if set.
func (c *CallbackCache) Callback(notificationID uuid.UUID) (int, time.Duration, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, found := c.callbacks[notificationID]
	return value.status, value.delay, found
}

// SetCallback replaces the response override for the notification.
func (c *CallbackCache) SetCallback(notificationID uuid.UUID, status int, delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callbacks[notificationID] = callbackOverride{status: status, delay: delay}
}

// ResetCallback clears the response override for the notification.
func (c *CallbackCache) ResetCallback(notificationID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.callbacks, notificationID)
}

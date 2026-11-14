// Package memory stores emulator overrides for concurrent requests.
package memory

import (
	"sync"
	"time"
)

// ClientCache stores the Information Service presets for each client.
type ClientCache struct {
	mu      sync.RWMutex
	clients map[string]clientOverride
}

type clientOverride struct {
	time  *time.Time
	price int64
}

// NewClientCache creates an empty cache keyed by client ID.
func NewClientCache() *ClientCache {
	return &ClientCache{clients: make(map[string]clientOverride)}
}

// Time returns a copy of the client's time override, or nil if unset.
func (c *ClientCache) Time(clientID string) *time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	preset := c.clients[clientID].time
	if preset == nil {
		return nil
	}
	value := *preset
	return &value
}

// Price returns the client's price in minor units, with zero as the default.
func (c *ClientCache) Price(clientID string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clients[clientID].price
}

// SetTime replaces the client's time override.
func (c *ClientCache) SetTime(clientID string, value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	preset := c.clients[clientID]
	preset.time = &value
	c.clients[clientID] = preset
}

// SetPrice replaces the client's price override.
func (c *ClientCache) SetPrice(clientID string, value int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	preset := c.clients[clientID]
	preset.price = value
	c.clients[clientID] = preset
}

// ResetClient clears both overrides for the client.
func (c *ClientCache) ResetClient(clientID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clients, clientID)
}

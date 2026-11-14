// Package memory stores emulator overrides for concurrent requests.
package memory

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store holds emulator overrides for the lifetime of the process.
type Store struct {
	mu        sync.RWMutex
	times     map[string]time.Time
	prices    map[string]int64
	callbacks map[uuid.UUID]callbackOverride
}

type callbackOverride struct {
	status int
	delay  time.Duration
}

// New creates an empty override store.
func New() *Store {
	return &Store{
		times:     make(map[string]time.Time),
		prices:    make(map[string]int64),
		callbacks: make(map[uuid.UUID]callbackOverride),
	}
}

// Time returns the client's time override, if set.
func (s *Store) Time(clientID string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, found := s.times[clientID]
	return value, found
}

// Price returns the client's price in minor units, with zero as the default.
func (s *Store) Price(clientID string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prices[clientID]
}

// SetTime replaces the client's time override.
func (s *Store) SetTime(clientID string, value time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.times[clientID] = value
}

// SetPrice replaces the client's price override.
func (s *Store) SetPrice(clientID string, value int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prices[clientID] = value
}

// ResetClient clears both overrides for the client.
func (s *Store) ResetClient(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.times, clientID)
	delete(s.prices, clientID)
}

// Callback returns the configured status and delay, if set.
func (s *Store) Callback(notificationID uuid.UUID) (int, time.Duration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, found := s.callbacks[notificationID]
	return value.status, value.delay, found
}

// SetCallback replaces the response override for the notification.
func (s *Store) SetCallback(notificationID uuid.UUID, status int, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks[notificationID] = callbackOverride{status: status, delay: delay}
}

// ResetCallback clears the response override for the notification.
func (s *Store) ResetCallback(notificationID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.callbacks, notificationID)
}

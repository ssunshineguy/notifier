package memory

import (
	"testing"
	"time"
)

func TestClientCacheTimeReturnsCopy(t *testing.T) {
	cache := NewClientCache()
	const clientID = "test-client"
	cache.SetPrice(clientID, 100)
	if got := cache.Time(clientID); got != nil {
		t.Fatalf("Time for a client with only a price = %v, want nil", got)
	}

	cache.SetTime(clientID, time.Time{})
	preset := cache.Time(clientID)
	if preset == nil || !preset.IsZero() {
		t.Fatalf("Time = %v, want a configured zero time", preset)
	}

	*preset = preset.Add(time.Hour)
	if got := cache.Time(clientID); got == nil || !got.IsZero() {
		t.Errorf("Time after modifying the returned value = %v, want zero time", got)
	}
}

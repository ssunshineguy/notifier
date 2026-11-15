package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestRunCollectorUsesOnlyItsDSNAndRequestsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stopped := false
	cfg := config.Config{
		Database:  config.Database{DSN: "postgres://collector@127.0.0.1:1/notifier?sslmode=disable", MaxConns: 1, OperationTimeoutSec: 1},
		Collector: config.Collector{BatchSize: 1000, RetentionDays: 365},
	}
	if err := RunCollector(ctx, cfg, func() { stopped = true }); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunCollector = %v, want context.Canceled", err)
	}
	if !stopped {
		t.Fatal("collector did not request process shutdown before pool closure")
	}
}

func TestCollectorRetentionUsesConfiguredDaysAcrossLeapYear(t *testing.T) {
	now := time.Date(2024, 3, 1, 3, 30, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	for _, tt := range []struct {
		days int32
		want time.Time
	}{
		{365, time.Date(2023, 3, 2, 0, 30, 0, 0, time.UTC)},
		{1, time.Date(2024, 2, 29, 0, 30, 0, 0, time.UTC)},
	} {
		got := collectorCutoff(now, tt.days)
		if !got.Equal(tt.want) || got.Location() != time.UTC {
			t.Errorf("cutoff for %d days = %v, want %v", tt.days, got, tt.want)
		}
	}
}

package application

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/collector"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

// RunCollector owns the database pool for one sequential cleanup invocation.
func RunCollector(ctx context.Context, cfg config.Config, requestStop context.CancelFunc) error {
	pool, err := newDatabasePool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("configure collector database: %w", err)
	}
	defer pool.Close()
	// Start the exit timer before closing the pool, including on failure.
	defer requestStop()

	store := postgres.NewStore(pool, time.Duration(cfg.Database.OperationTimeoutSec)*time.Second)
	before := collectorCutoff(time.Now(), int32(cfg.Collector.RetentionDays))
	slog.InfoContext(ctx, "starting notification collector", "batch_size", cfg.Collector.BatchSize,
		"retention_days", cfg.Collector.RetentionDays, "cutoff", before)
	deleted, err := collector.Run(ctx, store, int(cfg.Collector.BatchSize), before)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	slog.InfoContext(ctx, "notification collector finished", "deleted_count", deleted, "outcome", outcome)
	return err
}

func collectorCutoff(now time.Time, retentionDays int32) time.Time {
	return now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
}

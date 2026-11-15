package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/internal/health"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/callback"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/infoservice"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

// RunWorker owns the worker database pool, polling, and probe listener.
func RunWorker(ctx context.Context, cfg config.Config) error {
	poolConfig, err := pgxpool.ParseConfig(cfg.Database.WorkerDSN)
	if err != nil {
		return errors.New("invalid worker database configuration")
	}
	poolConfig.MinConns = cfg.Database.MinConns
	poolConfig.MaxConns = cfg.Database.MaxConns
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open worker database pool: %w", err)
	}
	defer pool.Close()

	informationService, err := infoservice.NewClient(cfg.InfoService)
	if err != nil {
		return fmt.Errorf("configure worker information service: %w", err)
	}
	callbackClient := callback.NewClient()
	databaseTimeout := time.Duration(cfg.Database.OperationTimeoutSec) * time.Second
	store := postgres.NewStore(pool, databaseTimeout)
	processing := worker.ProcessingSettings{
		ScheduledDelay:     time.Duration(cfg.Notification.ScheduledTimeSec) * time.Second,
		ResultWriteTimeout: databaseTimeout,
	}
	settings := worker.Settings{
		Workers:      cfg.Worker.Workers,
		BatchSize:    cfg.Worker.BatchSize,
		CycleTimeout: time.Duration(cfg.Worker.CycleTimeoutMS) * time.Millisecond,
		LockDuration: time.Duration(cfg.Notification.LockDurationMS) * time.Millisecond,
	}
	slog.InfoContext(ctx, "starting notification worker",
		"workers", cfg.Worker.Workers, "batch_size", cfg.Worker.BatchSize,
		"cycle_timeout_ms", cfg.Worker.CycleTimeoutMS, "lock_duration_ms", cfg.Notification.LockDurationMS)
	probes := middleware.LogAPIRequests(slog.Default(), health.NewHandler(store.CheckConnection))
	if err := health.RunWithProbes(ctx, "9082", probes, func(ctx context.Context) {
		worker.Run(ctx, store, func(ctx context.Context, claim core.NotificationClaim) {
			worker.ProcessNotification(ctx, claim, store, informationService, callbackClient, processing)
		}, settings)
	}); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}
	return nil
}

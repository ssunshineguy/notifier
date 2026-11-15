package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ssunshineguy/notifier/internal/health"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/callback"
	"github.com/ssunshineguy/notifier/notifier/internal/interactions/infoservice"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

const workerShutdownTimeout = 10 * time.Second

// RunWorker owns the worker database pool, polling, and probe listener.
func RunWorker(ctx context.Context, cfg config.Config) error {
	pool, err := newDatabasePool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("configure worker database: %w", err)
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	return runWorkerWithShutdown(ctx, pool.Close, func(processingCtx context.Context) error {
		informationService, err := infoservice.NewClient(cfg.InfoService)
		if err != nil {
			return fmt.Errorf("configure worker information service: %w", err)
		}
		callbackClient := callback.NewClient()
		databaseTimeout := time.Duration(cfg.Database.OperationTimeoutSec) * time.Second
		store := postgres.NewStore(pool, databaseTimeout)
		processing := worker.ProcessingSettings{
			TechnicalRetryDelay: time.Duration(cfg.Notification.TechnicalRetryDelaySec) * time.Second,
			ScheduledDelay:      time.Duration(cfg.Notification.ScheduledTimeSec) * time.Second,
			ResultWriteTimeout:  databaseTimeout,
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
			stopOnFailure := context.AfterFunc(ctx, stop)
			defer stopOnFailure()
			worker.Run(ctx, processingCtx, store, func(ctx context.Context, claim core.NotificationClaim) {
				worker.ProcessNotification(ctx, claim, store, informationService, callbackClient, processing)
			}, settings)
		}); err != nil {
			return fmt.Errorf("run worker: %w", err)
		}
		return nil
	})
}

func runWorkerWithShutdown(ctx context.Context, closePool func(), run func(context.Context) error) error {
	processingCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		runDone <- run(processingCtx)
	}()

	var runErr error
	select {
	case runErr = <-runDone:
		runDone = nil
	case <-ctx.Done():
	}

	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), workerShutdownTimeout)
	defer stop()
	if runDone != nil {
		select {
		case runErr = <-runDone:
		case <-shutdownCtx.Done():
			runErr = fmt.Errorf("drain worker: %w", shutdownCtx.Err())
		}
	}
	cancel()

	poolClosed := make(chan struct{})
	go func() {
		closePool()
		close(poolClosed)
	}()
	select {
	case <-poolClosed:
		return runErr
	case <-shutdownCtx.Done():
		return errors.Join(runErr, fmt.Errorf("close worker pool: %w", shutdownCtx.Err()))
	}
}

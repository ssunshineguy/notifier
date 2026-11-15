package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

type shutdownClaimFunc func(context.Context, int, time.Duration) ([]core.NotificationClaim, error)

func (f shutdownClaimFunc) ClaimNotifications(ctx context.Context, limit int, lease time.Duration) ([]core.NotificationClaim, error) {
	return f(ctx, limit, lease)
}

func TestWorkerShutdownBoundsStuckOperations(t *testing.T) {
	for _, operation := range []string{"claim", "handler"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				started := make(chan context.Context, 1)
				release := make(chan struct{})
				defer close(release)
				done := make(chan error, 1)
				var closes atomic.Int32
				store := shutdownClaimFunc(func(ctx context.Context, _ int, _ time.Duration) ([]core.NotificationClaim, error) {
					if operation == "claim" {
						started <- ctx
						<-release
					}
					return []core.NotificationClaim{{}}, nil
				})
				go func() {
					done <- runWorkerWithShutdown(ctx, func() { closes.Add(1) }, func(processingCtx context.Context) error {
						worker.Run(ctx, processingCtx, store, func(ctx context.Context, _ core.NotificationClaim) {
							started <- ctx
							<-release
						}, worker.Settings{Workers: 1, BatchSize: 1, CycleTimeout: time.Minute, LockDuration: time.Minute})
						return nil
					})
				}()
				operationCtx := <-started
				start := time.Now()
				stop()
				synctest.Wait()
				if operationCtx.Err() != nil {
					t.Fatal("signal canceled processing before the drain budget")
				}
				err := <-done
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 10*time.Second {
					t.Fatalf("shutdown = %v after %v, want deadline after 10s", err, time.Since(start))
				}
				if !errors.Is(operationCtx.Err(), context.Canceled) {
					t.Errorf("operation context = %v, want canceled after the shutdown budget", operationCtx.Err())
				}
				synctest.Wait()
				if closes.Load() != 1 {
					t.Errorf("pool closes = %d, want 1", closes.Load())
				}
			})
		})
	}
}

func TestWorkerShutdownSharesBudgetWithPoolClose(t *testing.T) {
	for _, closeDelay := range []time.Duration{time.Second, 3 * time.Second} {
		t.Run(closeDelay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				started := make(chan struct{})
				done := make(chan error, 1)
				poolFinished := make(chan struct{})
				var processingCtx context.Context
				go func() {
					done <- runWorkerWithShutdown(ctx, func() {
						if processingCtx.Err() == nil {
							t.Error("processing context still live when pool close started")
						}
						time.Sleep(closeDelay)
						close(poolFinished)
					}, func(workCtx context.Context) error {
						processingCtx = workCtx
						close(started)
						<-ctx.Done()
						time.Sleep(8 * time.Second)
						if workCtx.Err() != nil {
							t.Error("processing context canceled before drain completed")
						}
						return nil
					})
				}()
				<-started
				start := time.Now()
				stop()
				err := <-done
				wantElapsed := min(8*time.Second+closeDelay, 10*time.Second)
				if time.Since(start) != wantElapsed {
					t.Errorf("shutdown took %v, want %v including pool close", time.Since(start), wantElapsed)
				}
				if closeDelay == time.Second && err != nil {
					t.Errorf("graceful shutdown = %v, want nil", err)
				}
				if closeDelay == 3*time.Second && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("blocked pool shutdown = %v, want deadline", err)
				}
				<-poolFinished
			})
		})
	}
}

func TestWorkerShutdownClosesPoolOnStartupFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("startup failed")
		var processingCtx context.Context
		closes := 0
		err := runWorkerWithShutdown(t.Context(), func() { closes++ }, func(ctx context.Context) error {
			processingCtx = ctx
			return failure
		})
		if !errors.Is(err, failure) || closes != 1 || processingCtx.Err() == nil {
			t.Fatalf("shutdown = %v, closes = %d, processing error = %v", err, closes, processingCtx.Err())
		}
	})
}

func TestWorkerShutdownBoundsRealPoolClose(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://127.0.0.1/notifier?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MinConns, cfg.MaxConns = 1, 1
	connecting := make(chan struct{})
	release := make(chan struct{})
	cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		close(connecting)
		<-release
		return errors.New("connection attempt released by test")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(release)
		pool.Close()
	})
	select {
	case <-connecting:
	case <-time.After(2 * time.Second):
		t.Fatal("pool constructor did not start")
	}
	ctx, stop := context.WithCancel(t.Context())
	stop()
	start := time.Now()
	closed := make(chan struct{})
	err = runWorkerWithShutdown(ctx, func() {
		pool.Close()
		close(closed)
	}, func(context.Context) error { return nil })
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < 10*time.Second || elapsed > 11*time.Second {
		t.Fatalf("shutdown = %v after %v, want deadline within 10s plus 1s tolerance", err, elapsed)
	}
	select {
	case <-closed:
		t.Error("pool closed with its constructor still blocked")
	default:
	}
}

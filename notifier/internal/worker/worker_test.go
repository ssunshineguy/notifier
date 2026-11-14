package worker_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

type claimFunc func(context.Context, int, time.Duration) ([]core.NotificationClaim, error)

func (f claimFunc) ClaimNotifications(ctx context.Context, limit int, lockDuration time.Duration) ([]core.NotificationClaim, error) {
	return f(ctx, limit, lockDuration)
}

var defaultSettings = worker.Settings{
	Workers:      1,
	BatchSize:    1,
	CycleTimeout: 2 * time.Second,
	LockDuration: 2 * time.Second,
}

func TestRunStartsConfiguredWorkersAndBoundsEachBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := defaultSettings
		settings.Workers, settings.BatchSize = 2, 3
		workers, batchSize := settings.Workers, settings.BatchSize
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		claimCalls := make(chan struct{}, workers)
		processed := make(chan core.NotificationClaim, workers*batchSize)
		returnFromClaim := make(chan struct{})
		firstClaim := core.NotificationClaim{ID: uuid.New(), LockUntil: time.Now().Add(settings.LockDuration)}

		store := claimFunc(func(_ context.Context, limit int, lockDuration time.Duration) ([]core.NotificationClaim, error) {
			if limit != batchSize || lockDuration != settings.LockDuration {
				t.Errorf("claim parameters = %d, %v", limit, lockDuration)
			}
			claimCalls <- struct{}{}
			<-returnFromClaim
			return []core.NotificationClaim{firstClaim, {ID: uuid.New()}, {ID: uuid.New()}}, nil
		})
		process := func(ctx context.Context, claim core.NotificationClaim) {
			processed <- claim
			<-ctx.Done()
		}
		done := make(chan struct{})
		go func() {
			worker.Run(ctx, store, process, settings)
			close(done)
		}()

		synctest.Wait()
		if len(claimCalls) != workers || len(processed) != 0 {
			t.Errorf("before claim completed: %d workers, %d processors", len(claimCalls), len(processed))
		}
		close(returnFromClaim)
		synctest.Wait()
		if len(processed) != workers*batchSize {
			t.Errorf("active processors = %d, want %d", len(processed), workers*batchSize)
		}
		var fullClaims int
		for len(processed) > 0 {
			if <-processed == firstClaim {
				fullClaims++
			}
		}
		if fullClaims != workers {
			t.Errorf("processors received %d exact claims, want %d", fullClaims, workers)
		}
		cancel()
		<-done
	})
}

func TestRunImmediatelyClaimsAgainAfterProcessing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := time.Now()
		first := core.NotificationClaim{ID: uuid.New()}
		calls := 0
		processed := false
		store := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
			calls++
			if calls == 1 {
				return []core.NotificationClaim{first}, nil
			}
			if !processed || time.Since(start) != 0 {
				t.Errorf("next claim occurred before processing finished or waited: processed=%v elapsed=%v", processed, time.Since(start))
			}
			cancel()
			return nil, nil
		})

		worker.Run(ctx, store, func(_ context.Context, claim core.NotificationClaim) {
			if claim != first {
				t.Errorf("processed claim = %+v, want %+v", claim, first)
			}
			processed = true
		}, defaultSettings)
		if calls != 2 {
			t.Errorf("claims = %d, want 2", calls)
		}
	})
}

func TestRunCancelsBatchAndWaitsForSlowHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := defaultSettings
		settings.BatchSize = 2
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := time.Now()
		calls := 0
		slow := core.NotificationClaim{ID: uuid.New()}
		canceled := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		store := claimFunc(func(_ context.Context, limit int, _ time.Duration) ([]core.NotificationClaim, error) {
			calls++
			if calls == 1 {
				time.Sleep(500 * time.Millisecond)
				return []core.NotificationClaim{slow, {ID: uuid.New()}}, nil
			}
			if limit != settings.BatchSize {
				t.Errorf("next batch limit = %d, want %d", limit, settings.BatchSize)
			}
			cancel()
			return nil, nil
		})

		go func() {
			worker.Run(ctx, store, func(ctx context.Context, claim core.NotificationClaim) {
				if claim != slow {
					return
				}
				<-ctx.Done()
				if !errors.Is(ctx.Err(), context.DeadlineExceeded) || time.Since(start) != settings.CycleTimeout {
					t.Errorf("handler cancellation = %v after %v", ctx.Err(), time.Since(start))
				}
				close(canceled)
				<-release
			}, settings)
			close(done)
		}()

		<-canceled
		synctest.Wait()
		if calls != 1 {
			t.Errorf("claims while the slow handler waits = %d, want 1", calls)
		}
		close(release)
		<-done
		if calls != 2 {
			t.Errorf("claims = %d, want 2", calls)
		}
	})
}

func TestRunSkipsAClaimReturnedAfterDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		store := claimFunc(func(ctx context.Context, _ int, _ time.Duration) ([]core.NotificationClaim, error) {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return []core.NotificationClaim{{ID: uuid.New()}}, nil
			}
			cancel()
			return nil, nil
		})
		worker.Run(ctx, store, func(context.Context, core.NotificationClaim) {
			t.Error("processor ran after the cycle expired")
		}, defaultSettings)
		if calls != 2 {
			t.Errorf("claims = %d, want 2", calls)
		}
	})
}

func TestRunWaitsBeforeRetryingEmptyOrFailedClaim(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "empty"},
		{name: "database error", err: errors.New("claim failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cycleTimeout := defaultSettings.CycleTimeout
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				start := time.Now()
				calls := 0
				store := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
					calls++
					if calls == 1 {
						return nil, tt.err
					}
					if time.Since(start) != cycleTimeout {
						t.Errorf("worker retried after %v, want %v", time.Since(start), cycleTimeout)
					}
					cancel()
					return nil, nil
				})
				worker.Run(ctx, store, func(context.Context, core.NotificationClaim) {
					t.Error("processor ran without a successful claim")
				}, defaultSettings)
				if calls != 2 {
					t.Errorf("claims = %d, want 2", calls)
				}
			})
		})
	}
}

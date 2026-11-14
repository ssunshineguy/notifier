// Package worker runs bounded batches of due notifications.
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
)

// Store reserves due notifications for a batch.
type Store interface {
	ClaimNotifications(ctx context.Context, limit int, lockDuration time.Duration) ([]core.NotificationClaim, error)
}

// Settings controls polling, batch size and reservation duration.
type Settings struct {
	Workers      int
	BatchSize    int
	CycleTimeout time.Duration
	LockDuration time.Duration
}

// Run starts polling loops and joins their processors on shutdown.
func Run(ctx context.Context, store Store, process func(context.Context, core.NotificationClaim), settings Settings) {
	var loops sync.WaitGroup
	for range settings.Workers {
		loops.Go(func() {
			for ctx.Err() == nil {
				runCycle(ctx, store, process, settings)
			}
		})
	}
	loops.Wait()
}

func runCycle(ctx context.Context, store Store, process func(context.Context, core.NotificationClaim), settings Settings) {
	cycleCtx, cancel := context.WithTimeout(ctx, settings.CycleTimeout)
	defer cancel()

	batch, err := store.ClaimNotifications(cycleCtx, settings.BatchSize, settings.LockDuration)
	if err != nil || len(batch) == 0 {
		<-cycleCtx.Done()
		return
	}

	var tasks sync.WaitGroup
	for _, claim := range batch {
		if cycleCtx.Err() != nil {
			break
		}
		tasks.Go(func() {
			process(cycleCtx, claim)
		})
	}
	// The deadline asks handlers to exit so the next cycle can reuse their slots.
	// For simplicity, we wait for the whole batch and accept that a stuck goroutine can stall this worker.
	// We expect handlers to avoid mutexes and other waits that ignore cancellation.
	// If handlers hang in practice, we will run them in child processes we can terminate.
	tasks.Wait()
}

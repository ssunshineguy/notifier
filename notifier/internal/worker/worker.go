// Package worker runs bounded batches of due notifications.
package worker

import (
	"context"
	"sync"
	"sync/atomic"
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

// Run polls for notifications until ctx is canceled.
func Run(ctx context.Context, store Store, process func(context.Context, core.NotificationClaim), settings Settings) {
	var loops sync.WaitGroup
	for range settings.Workers {
		loops.Go(func() {
			var busy atomic.Int64
			for ctx.Err() == nil {
				cycleCtx, cancel := context.WithTimeout(ctx, settings.CycleTimeout)
				runCycle(cycleCtx, store, process, settings, &busy)
				<-cycleCtx.Done()
				cancel()
			}
		})
	}
	loops.Wait()
}

func runCycle(ctx context.Context, store Store, process func(context.Context, core.NotificationClaim), settings Settings, busy *atomic.Int64) {
	if ctx.Err() != nil {
		return
	}

	free := settings.BatchSize - int(busy.Load())
	if free == 0 {
		return
	}

	batch, err := store.ClaimNotifications(ctx, free, settings.LockDuration)
	if err != nil {
		return
	}

	for _, claim := range batch {
		if ctx.Err() != nil {
			return
		}
		busy.Add(1)
		go func() {
			defer busy.Add(-1)
			process(ctx, claim)
		}()
	}
}

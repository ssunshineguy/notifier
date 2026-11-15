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

// Run stops polling when ctx is canceled and lets handlers finish within their original cycle deadlines.
func Run(ctx context.Context, store Store, process func(context.Context, core.NotificationClaim), settings Settings) {
	cycleParent := context.WithoutCancel(ctx)
	var loops sync.WaitGroup
	for range settings.Workers {
		loops.Go(func() {
			var busy atomic.Int64
			var handlers sync.WaitGroup
			slotFreed := make(chan struct{}, 1)
			defer handlers.Wait()
			for ctx.Err() == nil {
				if int(busy.Load()) == settings.BatchSize {
					select {
					case <-ctx.Done():
						return
					case <-slotFreed:
						continue
					}
				}
				cycleCtx, cancel := context.WithTimeout(cycleParent, settings.CycleTimeout)
				batchDone := runCycle(ctx, cycleCtx, store, process, settings, &busy, &handlers, slotFreed)
				select {
				case <-ctx.Done():
					handlers.Wait()
				case <-cycleCtx.Done():
				case <-batchDone:
				}
				cancel()
			}
		})
	}
	loops.Wait()
}

func runCycle(stopCtx, ctx context.Context, store Store, process func(context.Context, core.NotificationClaim), settings Settings, busy *atomic.Int64, handlers *sync.WaitGroup, slotFreed chan<- struct{}) <-chan struct{} {
	if stopCtx.Err() != nil || ctx.Err() != nil {
		return nil
	}

	free := settings.BatchSize - int(busy.Load())
	if free == 0 {
		return nil
	}

	batch, err := store.ClaimNotifications(ctx, free, settings.LockDuration)
	if err != nil || len(batch) == 0 {
		return nil
	}

	var batchHandlers sync.WaitGroup
	for _, claim := range batch {
		if stopCtx.Err() != nil || ctx.Err() != nil {
			break
		}
		busy.Add(1)
		batchHandlers.Add(1)
		handlers.Go(func() {
			defer batchHandlers.Done()
			defer func() {
				busy.Add(-1)
				select {
				case slotFreed <- struct{}{}:
				default:
				}
			}()
			process(ctx, claim)
		})
	}

	batchDone := make(chan struct{})
	go func() {
		batchHandlers.Wait()
		close(batchDone)
	}()
	return batchDone
}

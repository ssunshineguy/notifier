package worker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

func TestRunStartsNextBatchOnlyAfterEveryCurrentHandlerFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := defaultSettings
		settings.BatchSize = 2
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		first, second := core.NotificationClaim{ID: uuid.New()}, core.NotificationClaim{ID: uuid.New()}
		started := make(chan context.Context, 2)
		releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		var nextClaimAt time.Time
		claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
			if calls.Add(1) == 1 {
				return []core.NotificationClaim{first, second}, nil
			}
			nextClaimAt = time.Now()
			stop()
			return nil, nil
		})
		done := make(chan struct{})
		go func() {
			worker.Run(ctx, claims, func(ctx context.Context, claim core.NotificationClaim) {
				started <- ctx
				if claim == first {
					<-releaseFirst
				} else {
					<-releaseSecond
				}
			}, settings)
			close(done)
		}()
		firstCtx, secondCtx := <-started, <-started
		close(releaseFirst)
		synctest.Wait()
		if calls := calls.Load(); calls != 1 {
			t.Errorf("claims after only one handler finished = %d, want 1", calls)
		}
		if firstCtx.Err() != nil || secondCtx.Err() != nil {
			t.Errorf("batch contexts canceled before all handlers finished: %v, %v", firstCtx.Err(), secondCtx.Err())
		}
		time.Sleep(500 * time.Millisecond)
		finishedAt := time.Now()
		close(releaseSecond)
		<-done
		if calls := calls.Load(); calls != 2 || !nextClaimAt.Equal(finishedAt) {
			t.Errorf("next claim at %v, calls = %d; want immediate claim at %v", nextClaimAt, calls, finishedAt)
		}
	})
}

func TestRunEmptyClaimAfterFastBatchStillWaitsForCycleDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		start := time.Now()
		calls := 0
		claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
			calls++
			switch calls {
			case 1:
				return []core.NotificationClaim{{ID: uuid.New()}}, nil
			case 2:
				if elapsed := time.Since(start); elapsed != 100*time.Millisecond {
					t.Errorf("claim after fast batch at %v, want 100ms", elapsed)
				}
				return nil, nil
			default:
				if elapsed := time.Since(start); elapsed != 100*time.Millisecond+defaultSettings.CycleTimeout {
					t.Errorf("retry after empty claim at %v, want 2.1s", elapsed)
				}
				stop()
				return nil, nil
			}
		})
		worker.Run(ctx, claims, func(context.Context, core.NotificationClaim) {
			time.Sleep(100 * time.Millisecond)
		}, defaultSettings)
		if calls != 3 {
			t.Errorf("claims = %d, want 3", calls)
		}
	})
}

func TestRunOldBatchCompletionDoesNotWakeCurrentCycle(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "active batch"
		if empty {
			name = "empty claim"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				settings := defaultSettings
				settings.BatchSize = 2
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				old := core.NotificationClaim{ID: uuid.New()}
				current := core.NotificationClaim{ID: uuid.New()}
				releaseOld, releaseCurrent := make(chan struct{}), make(chan struct{})
				secondClaim := make(chan struct{})
				currentStarted := make(chan context.Context, 1)
				var calls atomic.Int32
				start := time.Now()
				var nextClaimAt time.Time
				claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
					switch calls.Add(1) {
					case 1:
						return []core.NotificationClaim{old, {ID: uuid.New()}}, nil
					case 2:
						close(secondClaim)
						if empty {
							return nil, nil
						}
						return []core.NotificationClaim{current}, nil
					default:
						nextClaimAt = time.Now()
						stop()
						return nil, nil
					}
				})
				done := make(chan struct{})
				go func() {
					worker.Run(ctx, claims, func(ctx context.Context, claim core.NotificationClaim) {
						switch claim {
						case old:
							<-releaseOld
						case current:
							currentStarted <- ctx
							<-releaseCurrent
						}
					}, settings)
					close(done)
				}()
				<-secondClaim
				var currentCtx context.Context
				if !empty {
					currentCtx = <-currentStarted
				}
				time.Sleep(500 * time.Millisecond)
				close(releaseOld)
				synctest.Wait()
				if calls := calls.Load(); calls != 2 {
					t.Errorf("claims after old batch completed = %d, want 2", calls)
				}
				if currentCtx != nil && currentCtx.Err() != nil {
					t.Errorf("old batch completion canceled current batch: %v", currentCtx.Err())
				}
				wantNextIn := 2 * settings.CycleTimeout
				if !empty {
					time.Sleep(500 * time.Millisecond)
					close(releaseCurrent)
					wantNextIn = settings.CycleTimeout + time.Second
				}
				<-done
				if calls := calls.Load(); calls != 3 || nextClaimAt.Sub(start) != wantNextIn {
					t.Errorf("next claim after %v, calls = %d; want %v and 3", nextClaimAt.Sub(start), calls, wantNextIn)
				}
			})
		})
	}
}

func TestRunFastCurrentBatchDoesNotWaitForExpiredHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := defaultSettings
		settings.BatchSize = 2
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		old := core.NotificationClaim{ID: uuid.New()}
		current := core.NotificationClaim{ID: uuid.New()}
		releaseOld := make(chan struct{})
		nextClaim := make(chan struct{})
		calls := 0
		start := time.Now()
		claims := claimFunc(func(_ context.Context, limit int, _ time.Duration) ([]core.NotificationClaim, error) {
			calls++
			switch calls {
			case 1:
				return []core.NotificationClaim{old, {ID: uuid.New()}}, nil
			case 2:
				if limit != 1 {
					t.Errorf("current batch limit = %d, want 1 while old handler is running", limit)
				}
				return []core.NotificationClaim{current}, nil
			default:
				if elapsed := time.Since(start); elapsed != settings.CycleTimeout+100*time.Millisecond || limit != 1 {
					t.Errorf("next claim after %v with limit %d, want 2.1s with one free slot", elapsed, limit)
				}
				close(nextClaim)
				stop()
				return nil, nil
			}
		})
		done := make(chan struct{})
		go func() {
			worker.Run(ctx, claims, func(_ context.Context, claim core.NotificationClaim) {
				switch claim {
				case old:
					<-releaseOld
				case current:
					time.Sleep(100 * time.Millisecond)
				}
			}, settings)
			close(done)
		}()
		<-nextClaim
		close(releaseOld)
		<-done
		if calls != 3 {
			t.Errorf("claims = %d, want 3", calls)
		}
	})
}

func TestRunWaitsForFreeSlotAfterFullBatchExpires(t *testing.T) {
	for _, stopWhileFull := range []bool{false, true} {
		name := "claim freed slot"
		if stopWhileFull {
			name = "stop while full"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				settings := defaultSettings
				settings.BatchSize = 2
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				first, second := core.NotificationClaim{ID: uuid.New()}, core.NotificationClaim{ID: uuid.New()}
				started := make(chan context.Context, 2)
				releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
				nextClaim := make(chan time.Time, 1)
				var calls atomic.Int32
				claims := claimFunc(func(_ context.Context, limit int, _ time.Duration) ([]core.NotificationClaim, error) {
					if calls.Add(1) == 1 {
						return []core.NotificationClaim{first, second}, nil
					}
					if limit != 1 {
						t.Errorf("claim limit = %d, want only the released slot", limit)
					}
					nextClaim <- time.Now()
					stop()
					return nil, nil
				})
				done := make(chan struct{})
				go func() {
					worker.Run(ctx, claims, func(ctx context.Context, claim core.NotificationClaim) {
						started <- ctx
						if claim == first {
							<-releaseFirst
						} else {
							<-releaseSecond
						}
					}, settings)
					close(done)
				}()
				firstCtx, secondCtx := <-started, <-started
				time.Sleep(settings.CycleTimeout + time.Second)
				synctest.Wait()
				if calls := calls.Load(); calls != 1 {
					t.Errorf("claims while all expired handlers occupy slots = %d, want 1", calls)
				}
				if firstCtx.Err() != context.DeadlineExceeded || secondCtx.Err() != context.DeadlineExceeded {
					t.Errorf("expired batch context errors = %v, %v", firstCtx.Err(), secondCtx.Err())
				}
				if stopWhileFull {
					stop()
				}
				releasedAt := time.Now()
				close(releaseFirst)
				if !stopWhileFull {
					if claimedAt := <-nextClaim; !claimedAt.Equal(releasedAt) {
						t.Errorf("slot released at %v, next claim at %v; want immediate claim", releasedAt, claimedAt)
					}
				}
				synctest.Wait()
				wantCalls := int32(2)
				if stopWhileFull {
					wantCalls = 1
				}
				if calls := calls.Load(); calls != wantCalls {
					t.Errorf("claims after slot release = %d, want %d", calls, wantCalls)
				}
				close(releaseSecond)
				<-done
			})
		})
	}
}

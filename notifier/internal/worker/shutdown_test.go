package worker_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

type drainingProcessingStore struct {
	processingStoreStub
	save func(context.Context, core.NotificationClaim, core.NotificationResult) error
}

func (s *drainingProcessingStore) SaveNotificationResult(ctx context.Context, claim core.NotificationClaim, result core.NotificationResult) error {
	return s.save(ctx, claim, result)
}

func TestRunDrainsCallbackAndResultAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stopCtx, stop := context.WithCancel(t.Context())
		defer stop()
		claim := core.NotificationClaim{ID: uuid.New(), LockUntil: time.Now().Add(defaultSettings.LockDuration)}
		var claimCalls atomic.Int32
		claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
			claimCalls.Add(1)
			return []core.NotificationClaim{claim}, nil
		})
		callbackStarted := make(chan context.Context, 1)
		finishCallback := make(chan struct{})
		saveStarted := make(chan context.Context, 1)
		finishSave := make(chan struct{})
		store := &drainingProcessingStore{processingStoreStub: processingStoreStub{
			notification: core.NotificationConditions{NotificationStatus: core.EventSuccess},
		}}
		store.save = func(ctx context.Context, claim core.NotificationClaim, result core.NotificationResult) error {
			saveStarted <- ctx
			<-finishSave
			return store.processingStoreStub.SaveNotificationResult(ctx, claim, result)
		}
		callback := callbackFunc(func(ctx context.Context, _, _ string, event core.NotificationEvent) (int, error) {
			if event.Status != core.EventSuccess || event.NotificationID != claim.ID {
				t.Errorf("callback event = %+v, want saved success for %s", event, claim.ID)
			}
			callbackStarted <- ctx
			<-finishCallback
			return http.StatusOK, ctx.Err()
		})
		done := make(chan struct{})
		go func() {
			worker.Run(stopCtx, claims, func(ctx context.Context, claim core.NotificationClaim) {
				worker.ProcessNotification(ctx, claim, store, nil, callback, worker.ProcessingSettings{ResultWriteTimeout: time.Second})
			}, defaultSettings)
			close(done)
		}()

		callbackCtx := <-callbackStarted
		stop()
		synctest.Wait()
		if err := callbackCtx.Err(); err != nil {
			t.Errorf("callback canceled by stop: %v", err)
		}
		select {
		case <-saveStarted:
			t.Error("result write started before callback completed")
		default:
		}
		select {
		case <-done:
			t.Error("worker returned before callback completed")
		default:
		}

		close(finishCallback)
		saveCtx := <-saveStarted
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if err := saveCtx.Err(); err != nil {
			t.Errorf("result write canceled during drain: %v", err)
		}
		select {
		case <-done:
			t.Error("worker returned before result write completed")
		default:
		}
		close(finishSave)
		<-done
		if calls := claimCalls.Load(); calls != 1 {
			t.Errorf("claims = %d, want 1", calls)
		}
		if len(store.results) != 1 || store.savedClaim != claim || store.writeErr != nil {
			t.Fatalf("saved %v for %+v with error %v", store.results, store.savedClaim, store.writeErr)
		}
		if result := store.results[0]; result.NotificationStatus != core.EventSuccess || result.DeliveryStatus != core.DeliveryDelivered {
			t.Errorf("saved result = %+v, want delivered success", result)
		}
	})
}

func TestRunStopsClaimsAcrossCyclesWhileHandlerDrains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stopCtx, stop := context.WithCancel(t.Context())
		defer stop()
		settings := defaultSettings
		settings.BatchSize = 2
		var calls atomic.Int32
		claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
			calls.Add(1)
			return []core.NotificationClaim{{ID: uuid.New()}}, nil
		})
		started := make(chan context.Context, 1)
		release := make(chan struct{})
		done := make(chan struct{})
		go func() {
			worker.Run(stopCtx, claims, func(ctx context.Context, _ core.NotificationClaim) {
				started <- ctx
				<-release
			}, settings)
			close(done)
		}()

		cycleCtx := <-started
		time.Sleep(settings.CycleTimeout / 2)
		stoppedAt := time.Now()
		stop()
		<-cycleCtx.Done()
		if elapsed := time.Since(stoppedAt); elapsed != settings.CycleTimeout/2 {
			t.Errorf("cycle ended %v after stop, want the remaining %v", elapsed, settings.CycleTimeout/2)
		}
		time.Sleep(2 * settings.CycleTimeout)
		synctest.Wait()
		if calls := calls.Load(); calls != 1 {
			t.Errorf("claims after stop = %d, want 1", calls)
		}
		if cycleCtx.Err() != context.DeadlineExceeded {
			t.Errorf("cycle context error = %v, want deadline exceeded", cycleCtx.Err())
		}
		select {
		case <-done:
			t.Error("worker returned while expired handler was still running")
		default:
		}
		close(release)
		<-done
	})
}

func TestRunSkipsClaimReturnedAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stopCtx, stop := context.WithCancel(t.Context())
		defer stop()
		claimStarted := make(chan struct{})
		returnClaim := make(chan struct{})
		var calls atomic.Int32
		claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
			calls.Add(1)
			close(claimStarted)
			<-returnClaim
			return []core.NotificationClaim{{ID: uuid.New()}}, nil
		})
		done := make(chan struct{})
		go func() {
			worker.Run(stopCtx, claims, func(context.Context, core.NotificationClaim) {
				t.Error("processor started for a claim returned after stop")
			}, defaultSettings)
			close(done)
		}()
		<-claimStarted
		stop()
		close(returnClaim)
		<-done
		if calls := calls.Load(); calls != 1 {
			t.Errorf("claims = %d, want 1", calls)
		}
	})
}

func TestRunWithAlreadyCanceledStopDoesNotClaim(t *testing.T) {
	stopCtx, stop := context.WithCancel(t.Context())
	stop()
	claims := claimFunc(func(context.Context, int, time.Duration) ([]core.NotificationClaim, error) {
		t.Error("claim started after stop")
		return nil, nil
	})
	worker.Run(stopCtx, claims, func(context.Context, core.NotificationClaim) {
		t.Error("processor started after stop")
	}, defaultSettings)
}

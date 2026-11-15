package worker_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

func TestInitialDelayDoesNotChangeRecheckOrCallbackBackoff(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initialDelay time.Duration
		recheckDelay time.Duration
	}{
		{name: "immediate first check", recheckDelay: 7 * time.Second},
		{name: "hours before first check", initialDelay: 2 * time.Hour, recheckDelay: 19 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				created, err := core.NewNotification(core.Client{ID: "delay-test", MaxLifetime: 24 * time.Hour}, core.CreateRequest{
					ExternalID: "delay-test",
					Params:     []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}},
				}, time.Now(), tc.initialDelay)
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Until(created.NextScheduled))
				store := &processingStoreStub{notification: core.NotificationConditions{
					ClientID: created.ClientID, ExternalID: created.ExternalID, Params: created.Params,
					Deadline: created.Deadline, NotificationStatus: core.NotificationActive,
				}, target: core.CallbackTarget{URL: "http://example.test/notify"}}
				claim := core.NotificationClaim{ID: uuid.New(), LockUntil: time.Now().Add(time.Minute)}
				settings := worker.ProcessingSettings{ScheduledDelay: tc.recheckDelay, ResultWriteTimeout: time.Second}
				var sourceCalls, callbackCalls int
				price := int64(10)
				source := informationServiceStub{getPrice: func(context.Context, string, string) (int64, error) {
					sourceCalls++
					return price, nil
				}}
				callback := callbackFunc(func(_ context.Context, _, _ string, event core.NotificationEvent) (int, error) {
					callbackCalls++
					if event.Status != core.EventSuccess {
						t.Errorf("callback status = %s, want success", event.Status)
					}
					return http.StatusServiceUnavailable, errors.New("callback failure")
				})
				worker.ProcessNotification(t.Context(), claim, store, source, callback, settings)
				if len(store.results) != 1 || callbackCalls != 0 || sourceCalls != 1 {
					t.Fatalf("first check: results = %v, source calls = %d, callback calls = %d", store.results, sourceCalls, callbackCalls)
				}
				recheck := store.results[0]
				if recheck.NextScheduled == nil || !recheck.NextScheduled.Equal(time.Now().Add(tc.recheckDelay)) ||
					recheck.NotificationStatus != core.NotificationActive || recheck.FailedAttemptCount != 0 {
					t.Fatalf("recheck = %+v, want active at %v without callback attempts", recheck, time.Now().Add(tc.recheckDelay))
				}
				time.Sleep(time.Until(*recheck.NextScheduled))
				price = 11
				for attempt := 1; attempt <= 3; attempt++ {
					store.results = nil
					worker.ProcessNotification(t.Context(), claim, store, source, callback, settings)
					if len(store.results) != 1 {
						t.Fatalf("callback attempt %d: results = %v, want one saved result", attempt, store.results)
					}
					result := store.results[0]
					wantNext := time.Now()
					if attempt == 3 {
						wantNext = wantNext.Add(4 * time.Second)
					}
					if result.NextScheduled == nil || !result.NextScheduled.Equal(wantNext) ||
						result.NotificationStatus != core.EventSuccess || result.DeliveryStatus != core.DeliveryTryingToDeliver ||
						result.FailedAttemptCount != attempt || callbackCalls != attempt || sourceCalls != 2 {
						t.Fatalf("callback attempt %d: result = %+v, calls = %d, source calls = %d, want retry at %v", attempt, result, callbackCalls, sourceCalls, wantNext)
					}
					store.notification.NotificationStatus = result.NotificationStatus
					store.notification.FailedAttemptCount = result.FailedAttemptCount
				}
			})
		})
	}
}

func TestFirstCheckAfterDeadlineDeliversDeadlineExceeded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deadline := time.Now().Add(time.Minute)
		created, err := core.NewNotification(core.Client{ID: "delay-test", MaxLifetime: 24 * time.Hour}, core.CreateRequest{
			ExternalID: "expired-before-check", Deadline: &deadline,
			Params: []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}},
		}, time.Now(), 2*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(created.NextScheduled))
		store := &processingStoreStub{notification: core.NotificationConditions{
			ClientID: created.ClientID, ExternalID: created.ExternalID, Params: created.Params,
			Deadline: created.Deadline, NotificationStatus: core.NotificationActive,
		}, target: core.CallbackTarget{URL: "http://example.test/notify"}}
		source := informationServiceStub{getPrice: func(context.Context, string, string) (int64, error) {
			t.Error("conditions checked after the deadline")
			return 11, nil
		}}
		callbackCalls := 0
		callback := callbackFunc(func(_ context.Context, _, _ string, event core.NotificationEvent) (int, error) {
			callbackCalls++
			if event.Status != core.EventDeadlineExceeded || !event.SentAt.Equal(created.NextScheduled) {
				t.Errorf("callback = %+v, want deadline_exceeded at %v", event, created.NextScheduled)
			}
			return http.StatusOK, nil
		})
		worker.ProcessNotification(t.Context(), core.NotificationClaim{ID: uuid.New(), LockUntil: time.Now().Add(time.Minute)},
			store, source, callback, worker.ProcessingSettings{ScheduledDelay: 7 * time.Second, ResultWriteTimeout: time.Second})
		if callbackCalls != 1 || len(store.results) != 1 {
			t.Fatalf("callback calls = %d, results = %v, want one delivered event", callbackCalls, store.results)
		}
		result := store.results[0]
		if result.NotificationStatus != core.EventDeadlineExceeded || result.DeliveryStatus != core.DeliveryDelivered ||
			result.NextScheduled != nil || result.FailedAttemptCount != 0 {
			t.Errorf("saved result = %+v, want delivered deadline_exceeded without another scheduled check", result)
		}
	})
}

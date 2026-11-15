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

type processingStoreStub struct {
	notification      core.NotificationConditions
	loadErr           error
	dlqClaims         []core.NotificationClaim
	dlqErr            error
	rescheduled       []time.Time
	rescheduledClaims []core.NotificationClaim
	rescheduleErr     error
	target            core.CallbackTarget
	targetErr         error
	targetReads       int
	targetClaim       core.NotificationClaim
	results           []core.NotificationResult
	savedClaim        core.NotificationClaim
	writeErr          error
	saveErr           error
}

func (s *processingStoreStub) GetClaimedNotificationConditions(context.Context, core.NotificationClaim) (core.NotificationConditions, error) {
	return s.notification, s.loadErr
}

func (s *processingStoreStub) GetClaimedCallbackTarget(_ context.Context, claim core.NotificationClaim) (core.CallbackTarget, error) {
	s.targetReads++
	s.targetClaim = claim
	return s.target, s.targetErr
}

func (s *processingStoreStub) SaveNotificationResult(ctx context.Context, claim core.NotificationClaim, result core.NotificationResult) error {
	s.results = append(s.results, result)
	s.savedClaim = claim
	s.writeErr = ctx.Err()
	return errors.Join(s.writeErr, s.saveErr)
}

func TestProcessNotificationSavesNextState(t *testing.T) {
	callbackErr := errors.New("callback failure")
	sourceErr := errors.New("source unavailable")
	for _, tc := range []struct {
		name         string
		deadlineIn   time.Duration
		failed       int
		savedStatus  string
		conditionMet bool
		sourceErr    error
		sourceDelay  time.Duration
		callbackErr  error
		wantSource   bool
		wantEvent    string
		wantDelivery string
		wantFailures int
		wantNextIn   time.Duration
	}{
		{name: "matched", deadlineIn: time.Minute, conditionMet: true, wantSource: true, wantEvent: core.EventSuccess, wantDelivery: core.DeliveryDelivered},
		{name: "not matched", deadlineIn: time.Minute, wantSource: true, wantNextIn: 10 * time.Second},
		{name: "next check capped by deadline", deadlineIn: 3 * time.Second, wantSource: true, wantNextIn: 3 * time.Second},
		{name: "source unavailable", deadlineIn: time.Minute, sourceErr: sourceErr, wantSource: true, wantNextIn: 10 * time.Second},
		{name: "deadline reached", wantEvent: core.EventDeadlineExceeded, wantDelivery: core.DeliveryDelivered},
		{name: "source returns after deadline", deadlineIn: time.Second, conditionMet: true, sourceDelay: time.Second, wantSource: true, wantEvent: core.EventDeadlineExceeded, wantDelivery: core.DeliveryDelivered},
		{name: "retry skips condition check", deadlineIn: time.Minute, failed: 1, savedStatus: core.EventSuccess, wantEvent: core.EventSuccess, wantDelivery: core.DeliveryDelivered, wantFailures: 1},
		{name: "success retry keeps status past deadline", failed: 1, savedStatus: core.EventSuccess, wantEvent: core.EventSuccess, wantDelivery: core.DeliveryDelivered, wantFailures: 1},
		{name: "deadline event retry keeps status", failed: 1, savedStatus: core.EventDeadlineExceeded, wantEvent: core.EventDeadlineExceeded, wantDelivery: core.DeliveryDelivered, wantFailures: 1},
		{name: "initial callback rejected", deadlineIn: time.Minute, conditionMet: true, callbackErr: callbackErr, wantSource: true, wantEvent: core.EventSuccess, wantDelivery: core.DeliveryTryingToDeliver, wantFailures: 1},
		{name: "first backoff", failed: 2, savedStatus: core.EventSuccess, callbackErr: callbackErr, wantEvent: core.EventSuccess, wantDelivery: core.DeliveryTryingToDeliver, wantFailures: 3, wantNextIn: 4 * time.Second},
		{name: "attempts exhausted", failed: 41, savedStatus: core.EventDeadlineExceeded, callbackErr: callbackErr, wantEvent: core.EventDeadlineExceeded, wantFailures: 42, wantDelivery: core.DeliveryFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				claim := core.NotificationClaim{ID: uuid.New(), LockUntil: start.Add(time.Minute)}
				status := tc.savedStatus
				if status == "" {
					status = core.NotificationActive
				}
				store := &processingStoreStub{notification: core.NotificationConditions{
					ClientID: "worker-fixture", ExternalID: "event-fixture", Deadline: start.Add(tc.deadlineIn), FailedAttemptCount: tc.failed,
					NotificationStatus: status,
					Params:             []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}},
				}, target: core.CallbackTarget{URL: "http://example.test/notify"}}
				var sourceCalls, callbackCalls int
				source := informationServiceStub{getPrice: func(context.Context, string, string) (int64, error) {
					sourceCalls++
					time.Sleep(tc.sourceDelay)
					if tc.conditionMet {
						return 11, tc.sourceErr
					}
					return 10, tc.sourceErr
				}}
				callback := callbackFunc(func(_ context.Context, endpoint, requestID string, event core.NotificationEvent) (int, error) {
					callbackCalls++
					if len(store.results) != 0 {
						t.Error("result was saved before the callback")
					}
					if _, err := uuid.Parse(requestID); err != nil {
						t.Errorf("callback request ID = %q, want UUID", requestID)
					}
					if endpoint != store.target.URL || event.NotificationID != claim.ID || event.ExternalID != store.notification.ExternalID ||
						event.Status != tc.wantEvent || !event.SentAt.Equal(start.Add(tc.sourceDelay)) {
						t.Errorf("callback = %s %+v, want claim %s, event %s at %v", endpoint, event, claim.ID, tc.wantEvent, start)
					}
					if tc.callbackErr != nil {
						return http.StatusServiceUnavailable, tc.callbackErr
					}
					return http.StatusNoContent, nil
				})
				settings := worker.ProcessingSettings{ScheduledDelay: 10 * time.Second, ResultWriteTimeout: time.Second}
				worker.ProcessNotification(t.Context(), claim, store, source, callback, settings)

				if sourceCalls > 1 || (sourceCalls == 1) != tc.wantSource {
					t.Errorf("condition checks = %d, want check %t", sourceCalls, tc.wantSource)
				}
				if callbackCalls > 1 || (callbackCalls == 1) != (tc.wantEvent != "") || store.targetReads != callbackCalls ||
					(callbackCalls == 1 && store.targetClaim != claim) {
					t.Errorf("callback calls = %d, target reads = %d for %+v, want event %q for %+v", callbackCalls, store.targetReads, store.targetClaim, tc.wantEvent, claim)
				}
				if len(store.results) != 1 || store.savedClaim != claim || store.writeErr != nil {
					t.Fatalf("saved %v for claim %+v with error %v", store.results, store.savedClaim, store.writeErr)
				}
				result := store.results[0]
				wantNotificationStatus := tc.wantEvent
				if wantNotificationStatus == "" {
					wantNotificationStatus = core.NotificationActive
				}
				if result.NotificationStatus != wantNotificationStatus || result.DeliveryStatus != tc.wantDelivery ||
					result.FailedAttemptCount != tc.wantFailures {
					t.Errorf("result = %+v, want notification status %q, delivery status %q and %d failures",
						result, wantNotificationStatus, tc.wantDelivery, tc.wantFailures)
				}
				if tc.wantDelivery == core.DeliveryDelivered || tc.wantDelivery == core.DeliveryFailed {
					if result.NextScheduled != nil {
						t.Errorf("stopped notifications must have no schedule, got %v", *result.NextScheduled)
					}
				} else if result.NextScheduled == nil || !result.NextScheduled.Equal(start.Add(tc.wantNextIn)) {
					t.Errorf("next check = %v, want %v", result.NextScheduled, start.Add(tc.wantNextIn))
				}
			})
		})
	}
}

func TestCallbackTimeoutLeavesTimeToSaveAttempt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override time.Duration
		want     time.Duration
	}{
		{name: "default", want: time.Second},
		{name: "client override", override: 100 * time.Millisecond, want: 100 * time.Millisecond},
		{name: "budget capped by cycle", override: 5 * time.Second, want: 1500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				store := &processingStoreStub{notification: core.NotificationConditions{Deadline: start, NotificationStatus: core.NotificationActive}}
				if tc.override != 0 {
					store.target.Timeout = &tc.override
				}
				callback := callbackFunc(func(ctx context.Context, _, _ string, _ core.NotificationEvent) (int, error) {
					<-ctx.Done()
					if elapsed := time.Since(start); elapsed != tc.want {
						t.Errorf("callback timed out after %v, want %v", elapsed, tc.want)
					}
					return 0, ctx.Err()
				})
				worker.ProcessNotification(ctx, core.NotificationClaim{}, store, nil, callback, worker.ProcessingSettings{ResultWriteTimeout: 500 * time.Millisecond})
				if len(store.results) != 1 || store.results[0].FailedAttemptCount != 1 ||
					store.results[0].NotificationStatus != core.EventDeadlineExceeded ||
					store.results[0].DeliveryStatus != core.DeliveryTryingToDeliver || store.writeErr != nil {
					t.Errorf("after timeout saved %v with context error %v, want one failed deadline event", store.results, store.writeErr)
				}
			})
		})
	}
}

func TestMissingCallbackClientDoesNotCountAsAttempt(t *testing.T) {
	store := &processingStoreStub{notification: core.NotificationConditions{NotificationStatus: core.NotificationActive}, targetErr: core.ErrClientNotFound}
	worker.ProcessNotification(t.Context(), core.NotificationClaim{}, store, nil, nil, worker.ProcessingSettings{})
	if store.targetReads != 1 || len(store.results) != 0 {
		t.Errorf("target reads = %d, saved results = %v, want one read and no update", store.targetReads, store.results)
	}
}

func TestDeletedNotificationDoesNotStartCallback(t *testing.T) {
	store := &processingStoreStub{notification: core.NotificationConditions{NotificationStatus: core.NotificationActive}, targetErr: core.ErrNotificationNotFound}
	callback := callbackFunc(func(context.Context, string, string, core.NotificationEvent) (int, error) {
		t.Error("callback sent after the reservation was lost")
		return 0, nil
	})
	worker.ProcessNotification(t.Context(), core.NotificationClaim{}, store, nil, callback, worker.ProcessingSettings{})
	if len(store.results) != 0 {
		t.Errorf("saved results = %v, want none after the reservation was lost", store.results)
	}
}

func (s *processingStoreStub) MoveNotificationToDLQ(_ context.Context, claim core.NotificationClaim) error {
	s.dlqClaims = append(s.dlqClaims, claim)
	return s.dlqErr
}

func (s *processingStoreStub) RescheduleNotification(_ context.Context, claim core.NotificationClaim, next time.Time) error {
	s.rescheduled = append(s.rescheduled, next)
	s.rescheduledClaims = append(s.rescheduledClaims, claim)
	return s.rescheduleErr
}

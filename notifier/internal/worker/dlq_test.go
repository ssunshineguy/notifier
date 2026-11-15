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

func TestProcessingFailuresPreserveStateAndReschedule(t *testing.T) {
	transient := errors.New("temporary database failure")
	for _, tc := range []struct {
		name                       string
		loadErr, targetErr, dlqErr error
		wantDLQ, wantRetry         int
	}{
		{name: "invalid conditions", loadErr: core.ErrInvalidStoredConditions, wantDLQ: 1},
		{name: "invalid conditions write failure waits for lease", loadErr: core.ErrInvalidStoredConditions, dlqErr: transient, wantDLQ: 1},
		{name: "invalid conditions lost claim", loadErr: core.ErrInvalidStoredConditions, dlqErr: core.ErrNotificationNotFound, wantDLQ: 1},
		{name: "load database failure", loadErr: transient, wantRetry: 1},
		{name: "load lost claim", loadErr: core.ErrNotificationNotFound},
		{name: "missing client", targetErr: core.ErrClientNotFound, wantRetry: 1},
		{name: "target database failure", targetErr: transient, wantRetry: 1},
		{name: "target lost claim", targetErr: core.ErrNotificationNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now()
				claim := core.NotificationClaim{ID: uuid.New(), LockUntil: now.Add(time.Minute)}
				deadline := now.Add(-time.Hour)
				if errors.Is(tc.loadErr, core.ErrInvalidStoredConditions) {
					deadline = now.Add(time.Minute)
				}
				s := &processingStoreStub{notification: core.NotificationConditions{NotificationStatus: core.NotificationActive, Deadline: deadline, FailedAttemptCount: 7}, loadErr: tc.loadErr, targetErr: tc.targetErr, dlqErr: tc.dlqErr}
				worker.ProcessNotification(t.Context(), claim, s, nil, nil, worker.ProcessingSettings{TechnicalRetryDelay: 17 * time.Second})
				if len(s.results) != 0 || len(s.dlqClaims) != tc.wantDLQ || len(s.rescheduled) != tc.wantRetry {
					t.Fatalf("writes: results=%v dlq=%v reschedule=%v", s.results, s.dlqClaims, s.rescheduled)
				}
				if tc.wantDLQ == 1 && s.dlqClaims[0] != claim {
					t.Fatal("DLQ claim changed")
				}
				if tc.wantRetry == 1 && (s.rescheduledClaims[0] != claim || !s.rescheduled[0].Equal(now.Add(17*time.Second))) {
					t.Fatal("technical retry must preserve claim and ignore past deadline")
				}
			})
		})
	}
}

func TestTransientFailureWithCancelledContextLeavesRecoveryToLease(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := &processingStoreStub{loadErr: context.Canceled}
	worker.ProcessNotification(ctx, core.NotificationClaim{}, s, nil, nil, worker.ProcessingSettings{TechnicalRetryDelay: time.Second})
	if len(s.rescheduled) != 0 || len(s.results) != 0 || len(s.dlqClaims) != 0 {
		t.Fatal("cancelled work performed a write")
	}
}

func TestInvalidConditionsLoadedAcrossDeadlineEnterDLQ(t *testing.T) {
	claim := core.NotificationClaim{ID: uuid.New(), LockUntil: time.Now().Add(time.Minute)}
	s := &processingStoreStub{notification: core.NotificationConditions{NotificationStatus: core.NotificationActive, Deadline: time.Now().Add(-time.Second)}, loadErr: core.ErrInvalidStoredConditions}
	callback := callbackFunc(func(context.Context, string, string, core.NotificationEvent) (int, error) {
		t.Error("callback sent after invalid conditions were reported")
		return 200, nil
	})
	worker.ProcessNotification(t.Context(), claim, s, nil, callback, worker.ProcessingSettings{})
	if len(s.dlqClaims) != 1 || s.dlqClaims[0] != claim {
		t.Fatalf("DLQ claims = %+v, want the current claim", s.dlqClaims)
	}
	if s.targetReads != 0 || len(s.results) != 0 || len(s.rescheduled) != 0 {
		t.Fatal("invalid conditions continued delivery or scheduling after DLQ")
	}
}

func TestCallbackResultWriteFailureUsesTechnicalRetryWithoutAnotherAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		claim := core.NotificationClaim{ID: uuid.New(), LockUntil: now.Add(time.Minute)}
		store := &processingStoreStub{notification: core.NotificationConditions{NotificationStatus: core.EventSuccess, FailedAttemptCount: 4}, saveErr: errors.New("temporary write failure")}
		calls := 0
		callback := callbackFunc(func(context.Context, string, string, core.NotificationEvent) (int, error) { calls++; return 200, nil })
		worker.ProcessNotification(t.Context(), claim, store, nil, callback, worker.ProcessingSettings{TechnicalRetryDelay: 19 * time.Second})
		if calls != 1 || len(store.results) != 1 || store.results[0].FailedAttemptCount != 4 || store.results[0].NotificationStatus != core.EventSuccess || store.results[0].DeliveryStatus != core.DeliveryDelivered {
			t.Fatalf("callback/result=%d %+v", calls, store.results)
		}
		if len(store.rescheduled) != 1 || !store.rescheduled[0].Equal(now.Add(19*time.Second)) || store.rescheduledClaims[0] != claim {
			t.Fatal("result write failure did not reschedule current claim")
		}
		if len(store.dlqClaims) != 0 {
			t.Fatal("transient result write failure entered DLQ")
		}
	})
}

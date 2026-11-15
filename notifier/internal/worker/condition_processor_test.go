package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

type conditionStoreFunc func(context.Context, core.NotificationClaim) (core.NotificationConditions, error)

func (f conditionStoreFunc) GetClaimedNotificationConditions(ctx context.Context, claim core.NotificationClaim) (core.NotificationConditions, error) {
	return f(ctx, claim)
}

func (conditionStoreFunc) GetClaimedCallbackTarget(context.Context, core.NotificationClaim) (core.CallbackTarget, error) {
	return core.CallbackTarget{}, nil
}

func (conditionStoreFunc) SaveNotificationResult(context.Context, core.NotificationClaim, core.NotificationResult) error {
	return nil
}

type callbackFunc func(context.Context, string, string, core.NotificationEvent) (int, error)

func (f callbackFunc) Send(ctx context.Context, endpoint, requestID string, event core.NotificationEvent) (int, error) {
	return f(ctx, endpoint, requestID, event)
}

func acceptEvent(context.Context, string, string, core.NotificationEvent) (int, error) {
	return http.StatusNoContent, nil
}

type informationServiceStub struct {
	getTime  func(context.Context, string, string) (time.Time, error)
	getPrice func(context.Context, string, string) (int64, error)
}

func (s informationServiceStub) GetTime(ctx context.Context, clientID, requestID string) (time.Time, error) {
	return s.getTime(ctx, clientID, requestID)
}

func (s informationServiceStub) GetPrice(ctx context.Context, clientID, requestID string) (int64, error) {
	return s.getPrice(ctx, clientID, requestID)
}

func TestProcessConditions(t *testing.T) {
	after := time.Date(2050, time.January, 1, 9, 0, 0, 0, time.UTC)
	claim := core.NotificationClaim{ID: uuid.New(), LockUntil: after.Add(time.Minute)}
	const clientID = "test-client"
	const errorDetail = "private-service-detail"
	sourceErr := errors.New(errorDetail)
	timeCondition := core.Condition{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}}
	priceCondition := core.Condition{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: 5}}
	timeOnly := []core.Condition{timeCondition}
	priceOnly := []core.Condition{priceCondition}
	mixed := []core.Condition{timeCondition, {Type: core.ConditionAnd, Conditions: []core.Condition{
		timeCondition, priceCondition, priceCondition,
	}}}
	either := []core.Condition{{Type: core.ConditionOr, Conditions: []core.Condition{timeCondition, priceCondition}}}

	for _, tt := range []struct {
		name           string
		params         []core.Condition
		time           time.Time
		price          int64
		storeErr       error
		timeErr        error
		priceErr       error
		outcome        string
		wantMet        any
		wantTimeCalls  int
		wantPriceCalls int
	}{
		{name: "time met", params: timeOnly, time: after.Add(time.Second), wantMet: true, wantTimeCalls: 1},
		{name: "time not met", params: timeOnly, time: after, wantMet: false, wantTimeCalls: 1},
		{name: "price met", params: priceOnly, price: 0, wantMet: true, wantPriceCalls: 1},
		{name: "price not met", params: priceOnly, price: 5, wantMet: false, wantPriceCalls: 1},
		{name: "one call per source for nested conditions", params: mixed, time: after.Add(time.Second), price: 4, wantMet: true, wantTimeCalls: 1, wantPriceCalls: 1},
		{name: "claim unavailable", storeErr: core.ErrNotificationNotFound, outcome: "claim_unavailable"},
		{name: "load failed", storeErr: sourceErr, outcome: "load_failed"},
		{name: "clock unavailable", params: mixed, timeErr: sourceErr, outcome: "time_unavailable", wantTimeCalls: 1, wantPriceCalls: 1},
		{name: "price unavailable", params: priceOnly, priceErr: sourceErr, outcome: "price_unavailable", wantPriceCalls: 1},
		{name: "OR succeeds on price despite time error", params: either, price: 4, timeErr: sourceErr, wantMet: true, wantTimeCalls: 1, wantPriceCalls: 1},
		{name: "OR succeeds on time despite price error", params: either, time: after.Add(time.Second), priceErr: sourceErr, wantMet: true, wantTimeCalls: 1, wantPriceCalls: 1},
		{name: "OR cannot resolve without time", params: either, price: 5, timeErr: sourceErr, outcome: "time_unavailable", wantTimeCalls: 1, wantPriceCalls: 1},
		{name: "OR cannot resolve without price", params: either, time: after, priceErr: sourceErr, outcome: "price_unavailable", wantTimeCalls: 1, wantPriceCalls: 1},
		{name: "OR cannot resolve when both calls fail", params: either, timeErr: sourceErr, priceErr: sourceErr, outcome: "time_unavailable", wantTimeCalls: 1, wantPriceCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			parent, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			ctx := logging.WithLogger(parent, logger)
			parentDeadline, _ := parent.Deadline()
			timeCalls, priceCalls := 0, 0
			var timeRequestID, priceRequestID string
			store := conditionStoreFunc(func(ctx context.Context, got core.NotificationClaim) (core.NotificationConditions, error) {
				if got != claim {
					t.Errorf("claim = %+v, want %+v", got, claim)
				}
				if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(parentDeadline) {
					t.Errorf("store deadline = %v, want %v", deadline, parentDeadline)
				}
				return core.NotificationConditions{
					ClientID:           clientID,
					Params:             tt.params,
					Deadline:           after.Add(time.Minute),
					NotificationStatus: core.NotificationActive,
				}, tt.storeErr
			})
			checkRequest := func(ctx context.Context, gotClientID string) {
				if gotClientID != clientID {
					t.Errorf("client ID = %q, want %q", gotClientID, clientID)
				}
				if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(parentDeadline) {
					t.Errorf("source deadline = %v, want %v", deadline, parentDeadline)
				}
			}
			source := informationServiceStub{
				getTime: func(ctx context.Context, clientID, requestID string) (time.Time, error) {
					timeCalls++
					timeRequestID = requestID
					checkRequest(ctx, clientID)
					return tt.time, tt.timeErr
				},
				getPrice: func(ctx context.Context, clientID, requestID string) (int64, error) {
					priceCalls++
					priceRequestID = requestID
					checkRequest(ctx, clientID)
					return tt.price, tt.priceErr
				},
			}

			worker.ProcessNotification(ctx, claim, store, source, callbackFunc(acceptEvent), worker.ProcessingSettings{})

			if timeCalls != tt.wantTimeCalls || priceCalls != tt.wantPriceCalls {
				t.Errorf("time/price calls = %d/%d, want %d/%d", timeCalls, priceCalls, tt.wantTimeCalls, tt.wantPriceCalls)
			}
			var event struct {
				RequestID      string `json:"request_id"`
				NotificationID string `json:"notification_id"`
				Outcome        string `json:"outcome"`
				Met            any    `json:"met"`
			}
			firstLine, _, _ := bytes.Cut(logs.Bytes(), []byte("\n"))
			if err := json.Unmarshal(firstLine, &event); err != nil {
				t.Fatal(err)
			}
			if event.Outcome != tt.outcome || event.Met != tt.wantMet || event.NotificationID != claim.ID.String() {
				t.Errorf("log event = %+v, want outcome %q and met %v", event, tt.outcome, tt.wantMet)
			}
			if _, err := uuid.Parse(event.RequestID); err != nil {
				t.Errorf("log request ID = %q, parse error = %v", event.RequestID, err)
			}
			if timeCalls > 0 && event.RequestID != timeRequestID {
				t.Errorf("log request ID = %q, time request ID = %q", event.RequestID, timeRequestID)
			}
			if priceCalls > 0 && event.RequestID != priceRequestID {
				t.Errorf("log request ID = %q, price request ID = %q", event.RequestID, priceRequestID)
			}
			if bytes.Contains(logs.Bytes(), []byte(clientID)) || bytes.Contains(logs.Bytes(), []byte(errorDetail)) {
				t.Error("log contains private input or dependency error")
			}
		})
	}
}

func TestProcessConditionsFetchesTimeAndPriceConcurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan string, 2)
		releaseTime := make(chan struct{})
		releasePrice := make(chan struct{})
		done := make(chan struct{})
		store := conditionStoreFunc(func(context.Context, core.NotificationClaim) (core.NotificationConditions, error) {
			return core.NotificationConditions{
				ClientID:           "test-client",
				Params:             []core.Condition{{Type: core.ConditionTime}, {Type: core.ConditionPrice}},
				Deadline:           time.Now().Add(time.Minute),
				NotificationStatus: core.NotificationActive,
			}, nil
		})
		source := informationServiceStub{
			getTime: func(context.Context, string, string) (time.Time, error) {
				started <- "time"
				<-releaseTime
				return time.Now(), nil
			},
			getPrice: func(context.Context, string, string) (int64, error) {
				started <- "price"
				<-releasePrice
				return 1, nil
			},
		}

		go func() {
			defer close(done)
			worker.ProcessNotification(t.Context(), core.NotificationClaim{}, store, source, callbackFunc(acceptEvent), worker.ProcessingSettings{})
		}()
		synctest.Wait()
		if len(started) != 2 {
			t.Errorf("started requests = %d, want both before either returns", len(started))
		}
		close(releaseTime)
		synctest.Wait()
		select {
		case <-done:
			t.Error("worker finished before receiving the price")
		default:
		}
		close(releasePrice)
		<-done
	})
}

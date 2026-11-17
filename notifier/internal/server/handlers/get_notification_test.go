package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func (s *notificationStoreStub) GetNotification(ctx context.Context, notificationID uuid.UUID, clientID string) (core.NotificationDetails, error) {
	return s.getNotification(ctx, notificationID, clientID)
}

func TestGetNotification(t *testing.T) {
	const owner = "owner"
	const requestID = "request-42"
	id := uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71")
	unknownID := uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a72")
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	after := start.Add(time.Hour)
	deadline := after.Add(time.Hour)
	details := core.NotificationDetails{
		ID:         id,
		ExternalID: "order-42",
		Start:      start,
		Deadline:   deadline,
		Params: []core.Condition{{Type: core.ConditionAnd, Conditions: []core.Condition{
			{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
			{Type: core.ConditionOr, Conditions: []core.Condition{
				{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
				{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.LessThan, Threshold: after.Add(time.Minute)}},
				{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: math.MaxInt64}},
			}},
		}}},
	}
	dbErr := errors.New("database connection closed")

	tests := []struct {
		name               string
		id                 uuid.UUID
		clientID           string
		storeErr           error
		storedConditions   []core.Condition
		wantConditions     []core.Condition
		notificationStatus string
		deliveryStatus     string
		wantStatus         int
		wantType           string
		wantTitle          string
	}{
		{name: "active notification", id: id, clientID: owner, notificationStatus: "active", wantStatus: http.StatusOK},
		{name: "retrying after condition match", id: id, clientID: owner, notificationStatus: core.EventSuccess, deliveryStatus: core.DeliveryTryingToDeliver, wantStatus: http.StatusOK},
		{name: "delivered deadline event", id: id, clientID: owner, notificationStatus: core.EventDeadlineExceeded, deliveryStatus: core.DeliveryDelivered, wantStatus: http.StatusOK},
		{name: "delivery failed after condition match", id: id, clientID: owner, notificationStatus: core.EventSuccess, deliveryStatus: core.DeliveryFailed, wantStatus: http.StatusOK},
		{name: "foreign client", id: id, clientID: "another-client", wantStatus: http.StatusNotFound, wantType: "/problems/not-found", wantTitle: "Notification not found"},
		{name: "missing notification", id: unknownID, clientID: owner, wantStatus: http.StatusNotFound, wantType: "/problems/not-found", wantTitle: "Notification not found"},
		{name: "database error", id: id, clientID: owner, storeErr: dbErr, wantStatus: http.StatusInternalServerError, wantType: "/problems/internal-error", wantTitle: "Internal server error"},
		{name: "invalid condition type", id: id, clientID: owner, storedConditions: []core.Condition{{Type: core.ConditionAnd, Conditions: []core.Condition{{Type: "unknown"}}}}, wantStatus: http.StatusInternalServerError, wantType: "/problems/internal-error", wantTitle: "Internal server error"},
		{name: "unencodable timestamp", id: id, clientID: owner, storedConditions: []core.Condition{timeCondition(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))}, wantStatus: http.StatusInternalServerError, wantType: "/problems/internal-error", wantTitle: "Internal server error"},
		{
			name: "stored fractional timestamp", id: id, clientID: owner, notificationStatus: "active", wantStatus: http.StatusOK,
			storedConditions: []core.Condition{timeCondition(after.Add(987 * time.Millisecond))},
			wantConditions:   []core.Condition{timeCondition(after)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			store := &notificationStoreStub{
				getNotification: func(_ context.Context, notificationID uuid.UUID, clientID string) (core.NotificationDetails, error) {
					calls++
					if notificationID != tt.id || clientID != tt.clientID {
						t.Fatalf("lookup = (%s, %q), want (%s, %q)", notificationID, clientID, tt.id, tt.clientID)
					}
					if tt.storeErr != nil {
						return core.NotificationDetails{}, tt.storeErr
					}
					if notificationID != id || clientID != owner {
						return core.NotificationDetails{}, fmt.Errorf("lookup: %w", core.ErrNotificationNotFound)
					}
					notification := details
					notification.NotificationStatus = tt.notificationStatus
					notification.DeliveryStatus = tt.deliveryStatus
					if tt.storedConditions != nil {
						notification.Params = tt.storedConditions
					}
					return notification, nil
				},
			}
			server, err := codegen.NewServer(
				NewHandler(store, time.Second),
				middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: tt.clientID}}),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/notifications/"+tt.id.String(), nil)
			req.Header.Set("X-Client-ID", tt.clientID)
			req.Header.Set("X-Request-ID", requestID)
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)

			if calls != 1 {
				t.Fatalf("store called %d times, want 1", calls)
			}
			if resp.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", resp.Code, tt.wantStatus, resp.Body)
			}
			if got := resp.Header().Get("X-Request-ID"); got != requestID {
				t.Errorf("X-Request-ID = %q, want %q", got, requestID)
			}

			if tt.wantStatus == http.StatusOK {
				if got, _, err := mime.ParseMediaType(resp.Header().Get("Content-Type")); err != nil || got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", resp.Header().Get("Content-Type"))
				}
				var got codegen.Notification
				if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				wantConditions := tt.wantConditions
				if wantConditions == nil {
					wantConditions = details.Params
				}
				want := codegen.Notification{
					NotificationID:     codegen.NotificationId(id),
					ExternalID:         "order-42",
					NotificationStatus: codegen.NewOptOrdinaryNotificationStatus(codegen.OrdinaryNotificationStatus(tt.notificationStatus)),
					DeadlineDt:         deadline,
					StartDt:            start,
					NotificationParams: codegen.NotificationParams(wantConditions),
				}
				if tt.deliveryStatus != "" {
					want.DeliveryStatus.SetTo(codegen.DeliveryStatus(tt.deliveryStatus))
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("notification = %+v, want %+v", got, want)
				}
				return
			}

			if got := resp.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			var problem codegen.Problem
			if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Type != tt.wantType || problem.Title != tt.wantTitle || problem.Status != tt.wantStatus {
				t.Errorf("unexpected problem: %+v", problem)
			}
			if got, ok := problem.RequestID.Get(); !ok || got != requestID {
				t.Errorf("body request_id = %q, want %q", got, requestID)
			}
			if strings.Contains(resp.Body.String(), dbErr.Error()) || problem.Detail.IsSet() {
				t.Errorf("response includes internal details: %s", resp.Body)
			}
		})
	}
}

func TestGetNotificationInvalidID(t *testing.T) {
	const requestID = "invalid-id"
	client := core.Client{ID: "owner"}
	store := &notificationStoreStub{
		getNotification: func(context.Context, uuid.UUID, string) (core.NotificationDetails, error) {
			t.Fatal("invalid UUID reached the notification store")
			return core.NotificationDetails{}, nil
		},
	}
	server, err := codegen.NewServer(
		NewHandler(store, time.Second),
		middleware.NewClientSecurity(clientSourceStub{client: client}),
		codegen.WithErrorHandler(middleware.HandleError),
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/notifications/not-a-uuid", nil)
	req.Header.Set("X-Client-ID", client.ID)
	req.Header.Set("X-Request-ID", requestID)
	resp := httptest.NewRecorder()
	server.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.Code, resp.Body)
	}
	if got := resp.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if got := resp.Header().Get("X-Request-ID"); got != requestID {
		t.Errorf("X-Request-ID = %q, want %q", got, requestID)
	}
	var problem codegen.Problem
	if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != http.StatusBadRequest || problem.Type != "/problems/validation-error" {
		t.Errorf("unexpected problem: %+v", problem)
	}
	if got, ok := problem.RequestID.Get(); !ok || got != requestID {
		t.Errorf("body request_id = %q, want %q", got, requestID)
	}
}

func TestGetDLQNotification(t *testing.T) {
	id := uuid.New()
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	stopped := start.Add(time.Minute)
	const owner = "dlq-owner"
	for _, tc := range []struct {
		name       string
		change     func(*core.NotificationDetails)
		wantStatus int
	}{
		{"invalid conditions omitted", func(n *core.NotificationDetails) {
			n.Params = []core.Condition{{Type: "invalid-stored-type"}}
		}, http.StatusOK},
		{"unparsed conditions omitted", func(n *core.NotificationDetails) { n.Params = nil }, http.StatusOK},
		{"missing stop time", func(n *core.NotificationDetails) { n.Stop = nil }, http.StatusInternalServerError},
		{"missing DLQ time", func(n *core.NotificationDetails) { n.DLQAt = nil }, http.StatusInternalServerError},
		{"unsafe reason rejected", func(n *core.NotificationDetails) { n.DLQReason = "private-parser-value" }, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			details := core.NotificationDetails{
				ID: id, ExternalID: "dlq-order", NotificationStatus: core.NotificationDLQ,
				Start: start, Deadline: start.Add(time.Hour), Stop: &stopped, DLQAt: &stopped,
				DLQReason: core.DLQInvalidStoredConditions,
			}
			tc.change(&details)
			store := &notificationStoreStub{getNotification: func(_ context.Context, gotID uuid.UUID, gotOwner string) (core.NotificationDetails, error) {
				if gotID != id || gotOwner != owner {
					t.Fatalf("GET lookup did not preserve owner and ID")
				}
				return details, nil
			}}
			server, err := codegen.NewServer(NewHandler(store, time.Second), middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: owner}}))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/notifications/"+id.String(), nil)
			req.Header.Set("X-Client-ID", owner)
			req.Header.Set("X-Request-ID", "dlq-request")
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)
			if resp.Code != tc.wantStatus || resp.Header().Get("X-Request-ID") != "dlq-request" {
				t.Fatalf("response = %d, request ID = %q", resp.Code, resp.Header().Get("X-Request-ID"))
			}
			if tc.wantStatus != http.StatusOK {
				if strings.Contains(resp.Body.String(), "private-parser-value") {
					t.Fatal("unsafe DLQ reason leaked")
				}
				return
			}
			if got, _, err := mime.ParseMediaType(resp.Header().Get("Content-Type")); err != nil || got != "application/json" {
				t.Fatalf("Content-Type = %q", resp.Header().Get("Content-Type"))
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 8 || body["notification_params"] != nil || body["delivery_status"] != nil {
				t.Fatalf("DLQ response fields = %v", body)
			}
			var got codegen.NotificationRead
			if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.IsDLQNotification() {
				t.Fatal("response is not the DLQ schema variant")
			}
			want := codegen.DLQNotification{
				NotificationID: codegen.NotificationId(id), ExternalID: "dlq-order",
				NotificationStatus: codegen.DLQNotificationNotificationStatusDlq,
				DeadlineDt:         details.Deadline, StartDt: start, StopDt: stopped, DlqDt: stopped,
				DlqReason: codegen.DLQNotificationDlqReasonInvalidStoredConditions,
			}
			if !reflect.DeepEqual(got.DLQNotification, want) {
				t.Fatalf("DLQ response = %+v, want %+v", got.DLQNotification, want)
			}
		})
	}
}

func TestNotificationReadRequiresVariantFields(t *testing.T) {
	common := `"notification_id":"0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71","external_id":"contract","deadline_dt":"2026-12-01T18:00:00Z","start_dt":"2026-11-13T10:00:00Z"`
	for _, suffix := range []string{
		`,"notification_status":"active"`,
		`,"notification_status":"success","delivery_status":"trying_to_deliver"`,
		`,"notification_status":"dlq","notification_params":[]`,
		`,"notification_status":"dlq","stop_dt":"2026-11-13T10:00:10Z","dlq_dt":"2026-11-13T10:00:10Z"`,
		`,"notification_status":"dlq","stop_dt":"2026-11-13T10:00:10Z","dlq_dt":"2026-11-13T10:00:10Z","dlq_reason":"invalid_stored_conditions","delivery_status":"delivered"`,
	} {
		var got codegen.NotificationRead
		if err := json.Unmarshal([]byte("{"+common+suffix+"}"), &got); err == nil {
			t.Fatalf("invalid response accepted: %s", suffix)
		}
	}
}

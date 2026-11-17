package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (s *notificationStoreStub) UpdateNotification(ctx context.Context, id uuid.UUID, clientID string, conditions []core.Condition) (core.NotificationDetails, error) {
	return s.updateNotification(ctx, id, clientID, conditions)
}

func TestUpdateNotification(t *testing.T) {
	const requestID = "put-request-42"
	const owner = "owner"
	const condition = `{"type":"time","operation":"greater_than","value":"2026-12-02T09:00:00Z"}`
	const beforeCondition = `{"type":"time","operation":"less_than","value":"2026-12-02T17:00:00Z"}`
	const price = `{"type":"price","operation":"greater_than","value":0}`
	const validBody = `{"notification_params":[{"type":"or","conditions":[` + condition + `,` + beforeCondition + `,` + price + `]}]}`
	id := uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71")
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	deadline := time.Date(2026, 12, 2, 18, 0, 0, 0, time.UTC)
	after := time.Date(2026, 12, 2, 9, 0, 0, 0, time.UTC)
	before := deadline.Add(-time.Hour)
	expectedConditions := []core.Condition{{Type: core.ConditionOr, Conditions: []core.Condition{
		{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
		{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.LessThan, Threshold: before}},
		{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 0}},
	}}}
	dbErr := errors.New("private database failure")
	deepCondition := condition
	for range 4 {
		deepCondition = `{"type":"and","conditions":[` + deepCondition + `,` + condition + `]}`
	}
	groupOfSeven := `{"type":"and","conditions":[` + strings.Repeat(condition+",", 6) + condition + `]}`

	tests := []struct {
		name       string
		body       string
		id         string
		clientID   string
		storeErr   error
		calls      int
		status     int
		problem    string
		title      string
		fieldError bool
		retrying   bool
	}{
		{name: "replace conditions", calls: 1, status: 200},
		{name: "replace conditions during retry", calls: 1, status: 200, retrying: true},
		{
			name: "reserved by worker", storeErr: fmt.Errorf("update: %w", core.ErrNotificationReserved), calls: 1, status: http.StatusConflict,
			problem: "/problems/notification-in-progress", title: "Notification is being processed",
		},
		{name: "stopped", storeErr: fmt.Errorf("update: %w", core.ErrNotificationStopped), calls: 1, status: 400, problem: "/problems/incorrect-notification-status", title: "Incorrect notification status"},
		{name: "missing", storeErr: fmt.Errorf("update: %w", core.ErrNotificationNotFound), calls: 1, status: 404, problem: "/problems/not-found"},
		{name: "foreign", clientID: "other-client", storeErr: core.ErrNotificationNotFound, calls: 1, status: 404, problem: "/problems/not-found"},
		{name: "storage error", storeErr: dbErr, calls: 1, status: 500, problem: "/problems/internal-error"},
		{name: "no client header", status: 401, problem: "/problems/unauthorized"},
		{name: "invalid UUID", id: "not-a-uuid", status: 400, problem: "/problems/validation-error"},
		{name: "missing conditions", body: `{}`, status: 400, problem: "/problems/validation-error"},
		{name: "empty conditions", body: `{"notification_params":[]}`, status: 400, problem: "/problems/validation-error"},
		{name: "unsupported time operation", body: `{"notification_params":[{"type":"time","operation":"equal","value":"2026-12-02T09:00:00Z"}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "negative price threshold", body: `{"notification_params":[{"type":"price","operation":"greater_than","value":-1}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "null condition array", body: `{"notification_params":null}`, status: 400, problem: "/problems/validation-error"},
		{name: "case-sensitive condition fields", body: `{"notification_params":[{"Type":"time","operation":"greater_than","value":"2026-12-02T09:00:00Z"}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "unexpected condition field", body: `{"notification_params":[{"type":"time","operation":"greater_than","value":"2026-12-02T09:00:00Z","unexpected":true}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "leaf with group fields", body: `{"notification_params":[{"type":"price","operation":"greater_than","value":0,"conditions":[]}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "fractional price threshold", body: `{"notification_params":[{"type":"price","operation":"greater_than","value":1.5}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "duplicate discriminator", body: `{"notification_params":[{"type":"time","type":"price","operation":"greater_than","value":1}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "duplicate threshold", body: `{"notification_params":[{"type":"price","operation":"greater_than","value":"invalid","value":1}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "unencodable time offset", body: `{"notification_params":[{"type":"time","operation":"greater_than","value":"2026-12-02T09:00:00+24:00"}]}`, status: 400, problem: "/problems/validation-error"},
		{name: "reject deadline changes", body: `{"notification_params":[` + condition + `],"deadline_dt":"2026-12-02T20:00:00Z"}`, status: 400, problem: "/problems/validation-error"},
		{name: "group depth", body: `{"notification_params":[` + deepCondition + `]}`, status: 400, problem: "/problems/validation-error", fieldError: true},
		{name: "total leaves", body: `{"notification_params":[` + groupOfSeven + "," + groupOfSeven + "," + groupOfSeven + `]}`, status: 400, problem: "/problems/validation-error", fieldError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientID := tt.clientID
			if clientID == "" {
				clientID = owner
			}
			notificationStatus, deliveryStatus := core.NotificationActive, ""
			if tt.retrying {
				notificationStatus, deliveryStatus = core.EventSuccess, core.DeliveryTryingToDeliver
			}
			calls := 0
			store := &notificationStoreStub{
				updateNotification: func(_ context.Context, gotID uuid.UUID, gotClientID string, conditions []core.Condition) (core.NotificationDetails, error) {
					calls++
					if gotID != id || gotClientID != clientID || !reflect.DeepEqual(conditions, expectedConditions) {
						t.Errorf("unexpected update: %v, %q, %+v", gotID, gotClientID, conditions)
					}
					return core.NotificationDetails{
						ID: id, ExternalID: "order-42", Params: conditions,
						Start: start, Deadline: deadline, NotificationStatus: notificationStatus, DeliveryStatus: deliveryStatus,
					}, tt.storeErr
				},
			}
			server, err := codegen.NewServer(
				NewHandler(store, time.Second),
				middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: clientID}}),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}
			rawID := tt.id
			if rawID == "" {
				rawID = id.String()
			}
			body := tt.body
			if body == "" {
				body = validBody
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/v1/notifications/"+rawID, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", requestID)
			if tt.status != http.StatusUnauthorized {
				req.Header.Set("X-Client-ID", clientID)
			}
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)

			if calls != tt.calls || resp.Code != tt.status {
				t.Fatalf("store calls = %d, HTTP %d with %s, want %d calls and HTTP %d", calls, resp.Code, resp.Body, tt.calls, tt.status)
			}
			if got := resp.Header().Get("X-Request-ID"); got != requestID {
				t.Errorf("X-Request-ID = %q, want %q", got, requestID)
			}

			if tt.status == http.StatusOK {
				if mediaType, _, err := mime.ParseMediaType(resp.Header().Get("Content-Type")); err != nil || mediaType != "application/json" {
					t.Errorf("unexpected content type: %v", resp.Header())
				}
				var got codegen.Notification
				if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				want := codegen.Notification{
					NotificationID: codegen.NotificationId(id), ExternalID: "order-42",
					NotificationStatus: codegen.NewOptOrdinaryNotificationStatus(codegen.OrdinaryNotificationStatus(notificationStatus)),
					NotificationParams: codegen.NotificationParams(expectedConditions),
					DeadlineDt:         deadline, StartDt: start,
				}
				if deliveryStatus != "" {
					want.DeliveryStatus.SetTo(codegen.DeliveryStatus(deliveryStatus))
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("notification = %+v, want %+v", got, want)
				}
				return
			}

			if resp.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("unexpected content type: %v", resp.Header())
			}
			var problem codegen.Problem
			if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Type != tt.problem || problem.Status != tt.status || problem.RequestID.Or("") != requestID {
				t.Errorf("unexpected problem: %+v", problem)
			}
			if tt.title != "" && problem.Title != tt.title {
				t.Errorf("title = %q, want %q", problem.Title, tt.title)
			}
			if tt.fieldError && (len(problem.Errors) == 0 || !strings.HasPrefix(problem.Errors[0].Field, "/notification_params")) {
				t.Errorf("expected condition validation errors: %+v", problem)
			}
			if strings.Contains(resp.Body.String(), dbErr.Error()) || problem.Detail.IsSet() {
				t.Errorf("response exposes internal details: %s", resp.Body)
			}
		})
	}
}

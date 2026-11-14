package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

type clientSourceStub struct {
	client core.Client
	err    error
}

func (s clientSourceStub) GetClient(context.Context, string) (core.Client, error) {
	return s.client, s.err
}

func TestPrepareCreateNotification(t *testing.T) {
	client := core.Client{ID: "client-42", MaxLifetime: 24 * time.Hour}
	security := middleware.NewClientSecurity(clientSourceStub{client: client})
	ctx, err := security.HandleClientID(
		t.Context(),
		codegen.CreateNotificationOperation,
		codegen.ClientID{APIKey: client.ID},
	)
	if err != nil {
		t.Fatal(err)
	}

	after := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	later := after.Add(time.Hour)
	deadline := later.Add(time.Hour)

	req := codegen.NotificationCreate{
		ExternalID: "order-42",
		NotificationParams: codegen.NotificationParams{
			timeCondition(after),
			codegen.NewAndConditionCondition(codegen.AndCondition{
				Type: codegen.AndConditionTypeAnd,
				Conditions: []codegen.Condition{
					timeCondition(later),
					codegen.NewOrConditionCondition(codegen.OrCondition{
						Type:       codegen.OrConditionTypeOr,
						Conditions: []codegen.Condition{timeCondition(after), timeCondition(later)},
					}),
				},
			}),
		},
	}

	want := core.CreateRequest{
		ExternalID: "order-42",
		Params: []core.Condition{
			{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
			{
				Type: core.ConditionAnd,
				Conditions: []core.Condition{
					{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: later}},
					{
						Type: core.ConditionOr,
						Conditions: []core.Condition{
							{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
							{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: later}},
						},
					},
				},
			},
		},
	}

	t.Run("without deadline", func(t *testing.T) {
		gotClient, got, err := prepareCreateNotification(ctx, &req)
		if err != nil {
			t.Fatal(err)
		}
		if gotClient != client {
			t.Errorf("got client %+v, want %+v", gotClient, client)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got request %+v, want %+v", got, want)
		}
	})

	t.Run("with deadline", func(t *testing.T) {
		req := req
		req.DeadlineDt = codegen.NewOptDateTime(deadline)
		_, got, err := prepareCreateNotification(ctx, &req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Deadline == nil || !got.Deadline.Equal(deadline) {
			t.Errorf("got deadline %v, want %v", got.Deadline, deadline)
		}
	})

	t.Run("missing client context", func(t *testing.T) {
		if _, _, err := prepareCreateNotification(t.Context(), &req); err == nil {
			t.Fatal("expected an error when the client is missing")
		}
	})

	t.Run("unsupported condition type", func(t *testing.T) {
		req := codegen.NotificationCreate{NotificationParams: codegen.NotificationParams{{Type: "unsupported"}}}
		if _, _, err := prepareCreateNotification(ctx, &req); err == nil {
			t.Fatal("expected an error for an unsupported condition type")
		}
	})
}

func timeCondition(after time.Time) codegen.Condition {
	return codegen.NewTimeConditionCondition(codegen.TimeCondition{
		Type:      codegen.TimeConditionTypeTime,
		Operation: codegen.ComparisonOperationGreaterThan,
		Value:     after,
	})
}

type notificationStoreStub struct {
	id                 uuid.UUID
	saved              []core.Notification
	saveErr            error
	lookupErr          error
	getNotification    func(context.Context, uuid.UUID, string) (core.NotificationDetails, error)
	updateNotification func(context.Context, uuid.UUID, string, []core.Condition) (core.NotificationDetails, error)
	deleteNotification func(context.Context, uuid.UUID, string) error
}

func (s *notificationStoreStub) CreateNotification(ctx context.Context, notification core.Notification) (uuid.UUID, error) {
	if s.saveErr != nil {
		return uuid.Nil, s.saveErr
	}
	id, found, err := s.FindNotificationID(ctx, notification.ClientID, notification.ExternalID)
	if err != nil {
		return uuid.Nil, err
	}
	if found {
		return uuid.Nil, fmt.Errorf("insert notification: %w", &core.AlreadyExistsError{ID: id})
	}
	s.saved = append(s.saved, notification)
	return s.id, nil
}

func (s *notificationStoreStub) FindNotificationID(_ context.Context, clientID, externalID string) (uuid.UUID, bool, error) {
	if s.lookupErr != nil {
		return uuid.Nil, false, s.lookupErr
	}
	for _, notification := range s.saved {
		if notification.ClientID == clientID && notification.ExternalID == externalID {
			return s.id, true, nil
		}
	}
	return uuid.Nil, false, nil
}

func TestCreateNotification(t *testing.T) {
	const requestID = "request-42"
	const scheduledTime = 10 * time.Second
	client := core.Client{ID: "client-42", MaxLifetime: 24 * time.Hour}
	id := uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71")
	now := time.Now().UTC().Truncate(time.Second)
	threshold := now.Add(48 * time.Hour)

	tests := []struct {
		name       string
		operation  core.ComparisonOperation
		deadline   time.Time
		wantStatus int
		wantFields []codegen.FieldError
	}{
		{
			name:       "invalid deadline",
			operation:  core.GreaterThan,
			deadline:   time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
			wantStatus: http.StatusBadRequest,
			wantFields: []codegen.FieldError{
				{Field: "/deadline_dt", Message: "must be in the future"},
			},
		},
		{
			name:       "greater than",
			operation:  core.GreaterThan,
			wantStatus: http.StatusOK,
		},
		{
			name:       "explicit deadline before the time threshold",
			operation:  core.GreaterThan,
			deadline:   now.Add(time.Hour),
			wantStatus: http.StatusOK,
		},
		{
			name:       "less than",
			operation:  core.LessThan,
			wantStatus: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &notificationStoreStub{id: id}
			server, err := codegen.NewServer(
				NewHandler(store, scheduledTime),
				middleware.NewClientSecurity(clientSourceStub{client: client}),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}

			body := fmt.Sprintf(`{"external_id":"order-42","notification_params":[{"type":"time","operation":%q,"value":%q}]`,
				tt.operation, threshold.Format(time.RFC3339))
			if !tt.deadline.IsZero() {
				body += fmt.Sprintf(`,"deadline_dt":%q`, tt.deadline.Format(time.RFC3339))
			}
			body += "}"
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", requestID)
			req.Header.Set("X-Client-ID", client.ID)
			resp := httptest.NewRecorder()
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			middleware.LogAPIRequests(logger, server).ServeHTTP(resp, req)

			var checks struct {
				Message           string `json:"msg"`
				Outcome           string `json:"outcome"`
				InvalidFieldCount int    `json:"invalid_field_count"`
			}
			var foundChecks bool
			for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
				checks.Message, checks.Outcome, checks.InvalidFieldCount = "", "", 0
				if err := json.Unmarshal([]byte(line), &checks); err != nil {
					t.Fatal(err)
				}
				if checks.Message == "Notification Condition Checks" {
					foundChecks = true
					break
				}
			}
			wantOutcome := "passed"
			if tt.wantStatus == http.StatusBadRequest {
				wantOutcome = "failed"
			}
			logText := logs.String()
			hasConditionTree := strings.Contains(logText, `"notification_params":[{"type":"time"`)
			if !foundChecks || checks.Outcome != wantOutcome || checks.InvalidFieldCount != len(tt.wantFields) ||
				!strings.Contains(logText, `"external_id":"[redacted]"`) ||
				hasConditionTree != (tt.wantStatus == http.StatusOK) || strings.Contains(logText, "order-42") {
				t.Errorf("unexpected condition or request logs: %s", logText)
			}

			if resp.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", resp.Code, tt.wantStatus, resp.Body)
			}
			if got := resp.Header().Get("X-Request-ID"); got != requestID {
				t.Errorf("X-Request-ID = %q, want %q", got, requestID)
			}

			if tt.wantStatus == http.StatusOK {
				var created codegen.NotificationCreated
				if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
					t.Fatal(err)
				}
				if got := uuid.UUID(created.NotificationID); got != id {
					t.Errorf("notification_id = %s, want %s", got, id)
				}
				if len(store.saved) != 1 {
					t.Fatalf("saved %d notifications, want 1", len(store.saved))
				}
				saved := store.saved[0]
				if saved.ClientID != client.ID || saved.ExternalID != "order-42" {
					t.Errorf("unexpected saved notification: %+v", saved)
				}
				wantConditions := []core.Condition{{
					Type: core.ConditionTime,
					Time: core.TimeCondition{Operation: tt.operation, Threshold: threshold},
				}}
				if !reflect.DeepEqual(saved.Params, wantConditions) {
					t.Errorf("conditions = %+v, want %+v", saved.Params, wantConditions)
				}
				wantDeadline := tt.deadline
				if wantDeadline.IsZero() {
					wantDeadline = saved.Start.Add(client.MaxLifetime)
				}
				if !saved.Deadline.Equal(wantDeadline) {
					t.Errorf("deadline = %v, want %v", saved.Deadline, wantDeadline)
				}
				if !saved.NextScheduled.Equal(saved.Start.Add(scheduledTime)) {
					t.Errorf("next check = %v, want %v", saved.NextScheduled, saved.Start.Add(scheduledTime))
				}
				return
			}

			if len(store.saved) != 0 {
				t.Errorf("saved %d invalid notifications, want 0", len(store.saved))
			}
			if got := resp.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			var problem codegen.Problem
			if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Type != "/problems/validation-error" || problem.Status != http.StatusBadRequest {
				t.Errorf("unexpected problem: %+v", problem)
			}
			if got, ok := problem.RequestID.Get(); !ok || got != requestID {
				t.Errorf("body request_id = %q, want %q", got, requestID)
			}
			if !slices.Equal(problem.Errors, tt.wantFields) {
				t.Errorf("errors = %+v, want %+v", problem.Errors, tt.wantFields)
			}
		})
	}
}

func TestCreateNotificationDuplicate(t *testing.T) {
	client := core.Client{ID: "client-42", MaxLifetime: 24 * time.Hour}
	store := &notificationStoreStub{id: uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71")}
	server, err := codegen.NewServer(
		NewHandler(store, 10*time.Second),
		middleware.NewClientSecurity(clientSourceStub{client: client}),
	)
	if err != nil {
		t.Fatal(err)
	}

	requests := []struct {
		requestID  string
		after      string
		deadline   string
		wantStatus int
	}{
		{"first", "2001-01-01T00:00:00Z", "", http.StatusOK},
		{"repeat", "2002-01-01T00:00:00Z", "", http.StatusConflict},
		{"expired", "2002-01-01T00:00:00Z", `,"deadline_dt":"2003-01-01T00:00:00Z"`, http.StatusConflict},
	}
	for _, request := range requests {
		body := `{"external_id":"order-42","notification_params":[{"type":"time","operation":"greater_than","value":` +
			fmt.Sprintf("%q", request.after) + `}]` + request.deadline + "}"
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Client-ID", client.ID)
		req.Header.Set("X-Request-ID", request.requestID)
		resp := httptest.NewRecorder()
		server.ServeHTTP(resp, req)

		if resp.Code != request.wantStatus {
			t.Fatalf("%s: status = %d, want %d, body = %s", request.requestID, resp.Code, request.wantStatus, resp.Body)
		}
		if got := resp.Header().Get("X-Request-ID"); got != request.requestID {
			t.Errorf("X-Request-ID = %q, want %q", got, request.requestID)
		}
		var response codegen.NotificationCreated
		if err := json.Unmarshal(resp.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if got := uuid.UUID(response.NotificationID); got != store.id {
			t.Errorf("%s: notification_id = %s, want %s", request.requestID, got, store.id)
		}
	}

	if len(store.saved) != 1 {
		t.Fatalf("saved %d notifications, want 1", len(store.saved))
	}
	if got := store.saved[0].Params[0].Time.Threshold.Format(time.RFC3339); got != requests[0].after {
		t.Errorf("saved condition = %s, want original %s", got, requests[0].after)
	}
}

func TestCreateNotificationStorageError(t *testing.T) {
	const requestID = "request-42"
	client := core.Client{ID: "client-42", MaxLifetime: 24 * time.Hour}
	dbErr := errors.New("database connection lost")
	tests := []struct {
		name     string
		store    notificationStoreStub
		deadline string
	}{
		{name: "save", store: notificationStoreStub{saveErr: dbErr}},
		{
			name:     "lookup existing after validation failure",
			store:    notificationStoreStub{lookupErr: dbErr},
			deadline: `,"deadline_dt":"2000-01-01T00:00:00Z"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := codegen.NewServer(
				NewHandler(&tt.store, 10*time.Second),
				middleware.NewClientSecurity(clientSourceStub{client: client}),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}

			body := `{"external_id":"order-42","notification_params":[{"type":"time","operation":"greater_than","value":"2001-01-01T00:00:00Z"}]` + tt.deadline + "}"
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Client-ID", client.ID)
			req.Header.Set("X-Request-ID", requestID)
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)

			if resp.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body = %s", resp.Code, resp.Body)
			}
			if got := resp.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := resp.Header().Get("X-Request-ID"); got != requestID {
				t.Errorf("X-Request-ID = %q, want %q", got, requestID)
			}

			var problem map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"type":       "/problems/internal-error",
				"title":      "Internal server error",
				"status":     float64(http.StatusInternalServerError),
				"request_id": requestID,
			}
			if !reflect.DeepEqual(problem, want) {
				t.Errorf("problem = %+v, want %+v", problem, want)
			}
		})
	}
}

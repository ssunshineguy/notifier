package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func (s *notificationStoreStub) DeleteNotification(ctx context.Context, id uuid.UUID, clientID string) error {
	return s.deleteNotification(ctx, id, clientID)
}

func TestDeleteNotification(t *testing.T) {
	const owner = "owner"
	const requestID = "delete-request-42"
	id := uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71")
	unknownID := uuid.MustParse("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a72")
	dbErr := errors.New("private database failure")

	tests := []struct {
		name       string
		rawID      string
		clientID   string
		requestID  string
		storeErr   error
		lookupErr  error
		wantCalls  int
		wantStatus int
		problem    string
	}{
		{name: "delete owned notification", rawID: id.String(), clientID: owner, requestID: requestID, wantCalls: 1, wantStatus: http.StatusOK},
		{name: "missing", rawID: unknownID.String(), clientID: owner, requestID: requestID, wantCalls: 1, wantStatus: http.StatusNotFound, problem: "/problems/not-found"},
		{name: "foreign", rawID: id.String(), clientID: "other-client", requestID: requestID, wantCalls: 1, wantStatus: http.StatusNotFound, problem: "/problems/not-found"},
		{name: "no client header", rawID: id.String(), requestID: requestID, wantStatus: http.StatusUnauthorized, problem: "/problems/unauthorized"},
		{name: "unknown client", rawID: id.String(), clientID: "unknown", requestID: requestID, lookupErr: core.ErrClientNotFound, wantStatus: http.StatusUnauthorized, problem: "/problems/unauthorized"},
		{name: "invalid UUID", rawID: "not-a-uuid", clientID: owner, requestID: requestID, wantStatus: http.StatusBadRequest, problem: "/problems/validation-error"},
		{name: "missing request ID", rawID: id.String(), clientID: owner, wantStatus: http.StatusBadRequest, problem: "/problems/validation-error"},
		{name: "storage error", rawID: id.String(), clientID: owner, requestID: requestID, storeErr: dbErr, wantCalls: 1, wantStatus: http.StatusInternalServerError, problem: "/problems/internal-error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			store := &notificationStoreStub{
				deleteNotification: func(_ context.Context, notificationID uuid.UUID, clientID string) error {
					calls++
					if notificationID.String() != tt.rawID || clientID != tt.clientID {
						t.Errorf("unexpected delete: %s, %q", notificationID, clientID)
					}
					if tt.storeErr != nil {
						return tt.storeErr
					}
					if notificationID != id || clientID != owner {
						return fmt.Errorf("delete: %w", core.ErrNotificationNotFound)
					}
					return nil
				},
			}
			server, err := codegen.NewServer(
				NewHandler(store, time.Second),
				middleware.NewClientSecurity(clientSourceStub{client: core.Client{ID: tt.clientID}, err: tt.lookupErr}),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/v1/notifications/"+tt.rawID, nil)
			if tt.clientID != "" {
				req.Header.Set("X-Client-ID", tt.clientID)
			}
			if tt.requestID != "" {
				req.Header.Set("X-Request-ID", tt.requestID)
			}
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)

			if calls != tt.wantCalls {
				t.Errorf("store called %d times, want %d", calls, tt.wantCalls)
			}

			if resp.Code != tt.wantStatus {
				t.Fatalf("HTTP status = %d, want %d: %s", resp.Code, tt.wantStatus, resp.Body)
			}
			if got := resp.Header().Get("X-Request-ID"); got != tt.requestID {
				t.Errorf("X-Request-ID = %q, want %q", got, tt.requestID)
			}
			if tt.wantStatus == http.StatusOK {
				if resp.Body.Len() != 0 {
					t.Errorf("DELETE success has a body: %s", resp.Body)
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
			if problem.Status != tt.wantStatus || problem.Type != tt.problem || problem.RequestID.Or("") != tt.requestID {
				t.Errorf("unexpected problem: %+v", problem)
			}
			if problem.Detail.IsSet() || strings.Contains(resp.Body.String(), dbErr.Error()) {
				t.Errorf("response exposes internal details: %s", resp.Body)
			}
		})
	}
}

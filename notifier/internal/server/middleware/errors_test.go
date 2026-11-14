package middleware_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestClientSecurityErrors(t *testing.T) {
	const requestID = "request-42"
	dbErr := errors.New("database unavailable")

	tests := []struct {
		name       string
		clientID   string
		lookupErr  error
		body       string
		wantStatus int
		wantType   string
	}{
		{
			name:       "missing header",
			wantStatus: http.StatusUnauthorized,
			wantType:   "/problems/unauthorized",
		},
		{
			name:       "unknown client",
			clientID:   "unknown",
			lookupErr:  fmt.Errorf("lookup client: %w", core.ErrClientNotFound),
			wantStatus: http.StatusUnauthorized,
			wantType:   "/problems/unauthorized",
		},
		{
			name:       "malformed JSON",
			clientID:   "client-42",
			body:       `{"external_id":`,
			wantStatus: http.StatusBadRequest,
			wantType:   "/problems/validation-error",
		},
		{
			name:       "database error",
			clientID:   "client-42",
			lookupErr:  dbErr,
			wantStatus: http.StatusInternalServerError,
			wantType:   "/problems/internal-error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &clientSourceStub{err: tt.lookupErr}
			server, err := codegen.NewServer(
				codegen.UnimplementedHandler{},
				middleware.NewClientSecurity(source),
				codegen.WithErrorHandler(middleware.HandleError),
			)
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", requestID)
			if tt.clientID != "" {
				req.Header.Set("X-Client-ID", tt.clientID)
			}
			resp := httptest.NewRecorder()
			server.ServeHTTP(resp, req)

			if resp.Code != tt.wantStatus {
				t.Fatalf("HTTP status = %d, want %d; body = %s", resp.Code, tt.wantStatus, resp.Body)
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
			if problem.Status != tt.wantStatus || problem.Type != tt.wantType {
				t.Errorf("problem status/type = %d/%q, want %d/%q", problem.Status, problem.Type, tt.wantStatus, tt.wantType)
			}
			if got, ok := problem.RequestID.Get(); !ok || got != requestID {
				t.Errorf("problem request_id = %q, want %q", got, requestID)
			}
			if problem.Detail.IsSet() || strings.Contains(resp.Body.String(), dbErr.Error()) {
				t.Error("response exposes error details")
			}
		})
	}
}

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/internal/server/codegen"
)

func TestHealthProbes(t *testing.T) {
	store := &notificationStoreStub{}
	router, err := codegen.NewServer(NewHandler(store, time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name          string
		path          string
		connectionErr error
		wantStatus    int
	}{
		{"readiness with database", "/health/ready", nil, http.StatusOK},
		{"readiness without database", "/health/ready", errors.New("connection refused"), http.StatusServiceUnavailable},
		{"liveness without database", "/health/live", errors.New("connection refused"), http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store.connectionErr = tt.connectionErr
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", resp.Code, tt.wantStatus, resp.Body)
			}
			if resp.Code == http.StatusOK {
				var health codegen.Health
				if err := json.Unmarshal(resp.Body.Bytes(), &health); err != nil {
					t.Fatal(err)
				}
				if health.Status != codegen.HealthStatusOk {
					t.Errorf("health = %q, want ok", health.Status)
				}
				return
			}
			var problem codegen.Problem
			if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if resp.Header().Get("Content-Type") != "application/problem+json" ||
				problem.Type != "/problems/not-ready" || problem.Status != tt.wantStatus ||
				problem.Detail.Or("") != "database is unreachable" {
				t.Errorf("unexpected readiness response: %v %s", resp.Header(), resp.Body)
			}
		})
	}
}

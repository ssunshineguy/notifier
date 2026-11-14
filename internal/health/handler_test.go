package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbes(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		path       string
		checkReady func(context.Context) error
		wantStatus int
		wantType   string
		wantBody   string
	}{
		{
			name: "liveness skips readiness check", method: http.MethodGet, path: "/health/live",
			checkReady: func(context.Context) error { return errors.New("database unavailable") },
			wantStatus: http.StatusOK, wantType: "application/json; charset=utf-8", wantBody: `{"status":"ok"}`,
		},
		{
			name: "readiness without dependencies", method: http.MethodGet, path: "/health/ready",
			wantStatus: http.StatusOK, wantType: "application/json; charset=utf-8", wantBody: `{"status":"ok"}`,
		},
		{
			name: "readiness with database", method: http.MethodGet, path: "/health/ready",
			checkReady: func(context.Context) error { return nil },
			wantStatus: http.StatusOK, wantType: "application/json; charset=utf-8", wantBody: `{"status":"ok"}`,
		},
		{
			name: "readiness hides the database error", method: http.MethodGet, path: "/health/ready",
			checkReady: func(context.Context) error { return errors.New("connection refused") },
			wantStatus: http.StatusServiceUnavailable, wantType: "application/problem+json",
			wantBody: `{"type":"/problems/not-ready","title":"Service is not ready","status":503,"detail":"database is unreachable"}`,
		},
		{
			name: "unsupported method", method: http.MethodPost, path: "/health/ready",
			wantStatus: http.StatusMethodNotAllowed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), test.method, test.path, nil)
			res := httptest.NewRecorder()
			NewHandler(test.checkReady).ServeHTTP(res, req)
			if res.Code != test.wantStatus || res.Header().Get("Content-Type") != test.wantType ||
				res.Body.String() != test.wantBody {
				t.Fatalf("response = %d, %q, %s", res.Code, res.Header().Get("Content-Type"), res.Body)
			}
			if test.wantStatus == http.StatusMethodNotAllowed && res.Header().Get("Allow") != "GET" {
				t.Errorf("Allow = %q, want GET", res.Header().Get("Allow"))
			}
		})
	}
}

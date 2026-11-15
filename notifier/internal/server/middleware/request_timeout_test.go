package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestRequestTimeoutIncludesQueue(t *testing.T) {
	const timeout = 100 * time.Millisecond
	tests := []struct {
		name        string
		queueBudget time.Duration
		releaseAt   time.Duration
		wantElapsed time.Duration
		wantStatus  int
		wantStarted bool
	}{
		{"request expires in queue", 200 * time.Millisecond, 0, timeout, http.StatusServiceUnavailable, false},
		{"request expires after admission", 200 * time.Millisecond, 60 * time.Millisecond, timeout, http.StatusServiceUnavailable, true},
		{"queue budget expires first", 30 * time.Millisecond, 0, 30 * time.Millisecond, http.StatusTooManyRequests, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := make(chan time.Time, 1)
				limited := middleware.LimitInFlight(1, 1, tt.queueBudget, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Request-ID") == "held" {
						<-r.Context().Done()
						return
					}
					deadline, _ := r.Context().Deadline()
					started <- deadline
					<-r.Context().Done()
					w.WriteHeader(http.StatusNoContent)
				}))

				heldCtx, release := context.WithCancel(t.Context())
				defer release()
				held := httptest.NewRequestWithContext(heldCtx, http.MethodPost, "/v1/notifications", nil)
				held.Header.Set("X-Request-ID", "held")
				go limited.ServeHTTP(httptest.NewRecorder(), held)
				synctest.Wait()

				handler := middleware.RequestTimeout(timeout, time.Second, limited)
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", nil)
				req.Header.Set("X-Request-ID", "test-request")
				resp := httptest.NewRecorder()
				start := time.Now()
				if tt.releaseAt > 0 {
					time.AfterFunc(tt.releaseAt, release)
				}
				handler.ServeHTTP(resp, req)

				if resp.Code != tt.wantStatus || time.Since(start) != tt.wantElapsed {
					t.Fatalf("got HTTP %d after %v, want %d after %v", resp.Code, time.Since(start), tt.wantStatus, tt.wantElapsed)
				}
				var problem codegen.Problem
				if err := json.Unmarshal(resp.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if resp.Header().Get("Content-Type") != "application/problem+json" ||
					resp.Header().Get("X-Request-ID") != "test-request" ||
					problem.Status != tt.wantStatus || problem.RequestID.Or("") != "test-request" {
					t.Errorf("unexpected problem response: %v %s", resp.Header(), resp.Body)
				}
				if tt.wantStatus == http.StatusServiceUnavailable && problem.Type != "/problems/request-timeout" {
					t.Errorf("problem type = %q, want /problems/request-timeout", problem.Type)
				}
				select {
				case deadline := <-started:
					if !tt.wantStarted || !deadline.Equal(start.Add(timeout)) {
						t.Errorf("handler started with deadline %v, want started=%t and deadline %v", deadline, tt.wantStarted, start.Add(timeout))
					}
				default:
					if tt.wantStarted {
						t.Error("request never reached the handler")
					}
				}
			})
		})
	}
}

func TestRequestTimeoutPreservesResponse(t *testing.T) {
	handler := middleware.RequestTimeout(time.Second, time.Second, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-ID", "normal-request")
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("created")); err != nil {
			t.Error(err)
		}
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/notifications", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated || resp.Body.String() != "created" ||
		resp.Header().Get("X-Request-ID") != "normal-request" {
		t.Errorf("unexpected response: HTTP %d %v %s", resp.Code, resp.Header(), resp.Body)
	}
}

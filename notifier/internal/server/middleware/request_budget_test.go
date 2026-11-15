package middleware_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestRequestTimeoutKeepsProcessingAndWriteBudgetsSeparate(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		parent     time.Duration
		processing time.Duration
		wantTime   time.Duration
		wantStatus int
	}{
		{"success", time.Second, 200 * time.Millisecond, 200 * time.Millisecond, http.StatusOK},
		{"request timeout", time.Second, time.Second, 300 * time.Millisecond, http.StatusServiceUnavailable},
		{"shorter parent", 80 * time.Millisecond, time.Second, 80 * time.Millisecond, http.StatusServiceUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				parent, cancel := context.WithTimeout(t.Context(), scenario.parent)
				defer cancel()
				deadline := start.Add(min(300*time.Millisecond, scenario.parent))
				next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					operation, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					defer cancel()
					if got, ok := operation.Deadline(); !ok || !got.Equal(deadline) {
						t.Errorf("nested operation deadline = %v, want %v", got, deadline)
					}
					timer := time.NewTimer(scenario.processing)
					defer timer.Stop()
					select {
					case <-operation.Done():
					case <-timer.C:
					}
					w.WriteHeader(http.StatusOK)
				})
				response := &deadlineResponse{ResponseRecorder: httptest.NewRecorder()}
				request := httptest.NewRequestWithContext(parent, http.MethodGet, "/", nil)
				middleware.RequestTimeout(300*time.Millisecond, time.Second, next).ServeHTTP(response, request)
				if response.Code != scenario.wantStatus || time.Since(start) != scenario.wantTime {
					t.Fatalf("status=%d elapsed=%v, want status=%d elapsed=%v", response.Code, time.Since(start), scenario.wantStatus, scenario.wantTime)
				}
				if !response.read.Equal(deadline) || !response.write.Equal(start.Add(scenario.wantTime+time.Second)) {
					t.Fatalf("deadlines read=%v write=%v, want read=%v write=%v", response.read, response.write, deadline, start.Add(scenario.wantTime+time.Second))
				}
			})
		})
	}
}

type deadlineResponse struct {
	*httptest.ResponseRecorder
	read  time.Time
	write time.Time
}

// SetReadDeadline records the body deadline without real I/O in the virtual-time test.
func (w *deadlineResponse) SetReadDeadline(deadline time.Time) error {
	w.read = deadline
	return nil
}

// SetWriteDeadline records when the middleware permits the final response to be written.
func (w *deadlineResponse) SetWriteDeadline(deadline time.Time) error {
	w.write = deadline
	return nil
}

func TestRequestTimeoutSlowBodyReturnsProblemTCP(t *testing.T) {
	testRequestTimeoutTCP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}), "POST / HTTP/1.1\r\nHost: local\r\nX-Request-ID: tcp-timeout\r\nContent-Length: 1024\r\n\r\n")
}

func TestRequestTimeoutSlowProcessingReturnsProblemTCP(t *testing.T) {
	testRequestTimeoutTCP(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}), "GET / HTTP/1.1\r\nHost: local\r\nX-Request-ID: tcp-timeout\r\n\r\n")
}

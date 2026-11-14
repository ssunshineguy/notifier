package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func TestLimitInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") == "held" {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := middleware.LimitInFlight(1, 1, 0, next)

	send := func(path, requestID string) <-chan *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
		req.Header.Set("X-Request-ID", requestID)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			done <- resp
		}()
		return done
	}
	wait := func(done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		t.Helper()
		select {
		case resp := <-done:
			return resp
		case <-time.After(5 * time.Second):
			t.Fatal("request did not finish")
			return nil
		}
	}

	const notifications = "/v1/notifications"
	held := send(notifications, "held")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach the handler")
	}

	rejected := wait(send(notifications, "rejected"))
	if rejected.Code != http.StatusTooManyRequests {
		t.Fatalf("excess request status = %d, want 429", rejected.Code)
	}
	if got := rejected.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if got := rejected.Header().Get("X-Request-ID"); got != "rejected" {
		t.Errorf("X-Request-ID = %q, want rejected", got)
	}
	var problem codegen.Problem
	if err := json.Unmarshal(rejected.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Status != http.StatusTooManyRequests || problem.Type != "/problems/too-many-requests" ||
		problem.RequestID.Or("") != "rejected" {
		t.Errorf("unexpected overload response: %+v", problem)
	}

	unblock()
	if got := wait(held).Code; got != http.StatusNoContent {
		t.Errorf("first request status = %d, want 204", got)
	}
	if got := wait(send(notifications, "next")).Code; got != http.StatusNoContent {
		t.Errorf("next request status = %d, want 204", got)
	}
}

func TestLimitInFlightWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const timeout = 100 * time.Millisecond
		release := make(chan struct{})
		handler := middleware.LimitInFlight(1, 2, timeout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Request-ID") == "held" {
				<-release
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		send := func(ctx context.Context, id string) <-chan int {
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/notifications", nil)
			req.Header.Set("X-Request-ID", id)
			result := make(chan int, 1)
			go func() {
				resp := httptest.NewRecorder()
				handler.ServeHTTP(resp, req)
				result <- resp.Code
			}()
			synctest.Wait()
			return result
		}

		held := send(t.Context(), "held")
		start := time.Now()
		firstQueued := send(t.Context(), "first-queued")
		secondQueued := send(t.Context(), "second-queued")
		if got := <-send(t.Context(), "queue-full"); got != http.StatusTooManyRequests || !time.Now().Equal(start) {
			t.Fatalf("queue full returned %d after %v, want immediate 429", got, time.Since(start))
		}
		for _, queued := range []<-chan int{firstQueued, secondQueued} {
			if got := <-queued; got != http.StatusTooManyRequests || time.Since(start) != timeout {
				t.Fatalf("timeout returned %d after %v, want 429 after %v", got, time.Since(start), timeout)
			}
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancelled := send(ctx, "cancelled")
		cancel()
		if got := <-cancelled; got != http.StatusOK {
			t.Fatalf("cancelled request wrote status %d, want untouched recorder", got)
		}

		admitted := send(t.Context(), "admitted")
		select {
		case got := <-admitted:
			t.Fatalf("waiting request finished before the slot was released: %d", got)
		default:
		}
		close(release)
		if got := <-held; got != http.StatusNoContent {
			t.Errorf("first request status = %d, want 204", got)
		}
		if got := <-admitted; got != http.StatusNoContent {
			t.Errorf("waiting request status = %d, want 204", got)
		}
	})
}

package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

const (
	clientOverrides   = "/v1/client-overrides/"
	callbackOverrides = "/v1/callback-overrides/"
	firstNotification = "0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71"
	otherNotification = "0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a72"
)

func TestClientOverrides(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router, err := NewRouter()
		if err != nil {
			t.Fatal(err)
		}
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"time","value":"2026-12-01T12:00:00.123456789+03:00"}`, 200, "")
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"price","value":19990}`, 200, "")
		request(t, router, http.MethodPost, clientOverrides+"bob", "", `{"parameter":"price","value":9007199254740993}`, 200, "")
		request(t, router, http.MethodGet, "/time", "alice", "", 200, `{"time":"2026-12-01T09:00:00.123456789Z"}`)
		request(t, router, http.MethodGet, "/price", "alice", "", 200, `{"price":19990}`)
		request(t, router, http.MethodGet, "/price", "bob", "", 200, `{"price":9007199254740993}`)
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"price","value":199.90}`, 400, "")
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"time","value":"tomorrow"}`, 400, "")
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"time","value":"0000-01-01T00:00:00+01:00"}`, 400, "")
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"other","value":42}`, 400, "")
		request(t, router, http.MethodGet, "/price", "alice", "", 200, `{"price":19990}`)
		request(t, router, http.MethodPost, clientOverrides+"alice", "", `{"parameter":"price","value":5}`, 200, "")
		request(t, router, http.MethodGet, "/price", "alice", "", 200, `{"price":5}`)
		request(t, router, http.MethodGet, "/time", "alice", "", 200, `{"time":"2026-12-01T09:00:00.123456789Z"}`)

		request(t, router, http.MethodDelete, clientOverrides+"alice", "", "", 200, "")
		request(t, router, http.MethodDelete, clientOverrides+"alice", "", "", 200, "")
		request(t, router, http.MethodGet, "/price", "alice", "", 200, `{"price":0}`)
		request(t, router, http.MethodGet, "/price", "bob", "", 200, `{"price":9007199254740993}`)
		defaultTime := fmt.Sprintf(`{"time":%q}`, time.Now().UTC().Format(time.RFC3339Nano))
		request(t, router, http.MethodGet, "/time", "alice", "", 200, defaultTime)
	})
}

func TestCallbackOverrides(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router, err := NewRouter()
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range []int{200, 201, 400, 404, 500, 503} {
			override := fmt.Sprintf(`{"status_code":%d,"response_timeout":25}`, code)
			request(t, router, http.MethodPost, callbackOverrides+firstNotification, "", override, 200, "")
			for range 2 {
				start := time.Now()
				request(t, router, http.MethodPost, "/callback", "", event(firstNotification), code, "")
				if elapsed := time.Since(start); elapsed != 25*time.Millisecond {
					t.Errorf("callback delay = %v, want 25ms", elapsed)
				}
			}
		}

		request(t, router, http.MethodPost, "/callback", "", event(otherNotification), 200, "")
		request(t, router, http.MethodPost, "/callback", "", `{"notification_id":"invalid"}`, 400, "")
		request(t, router, http.MethodDelete, callbackOverrides+"invalid", "", "", 400, "")
		for _, invalid := range []string{
			`{"status_code":202,"response_timeout":0}`,
			`{"status_code":200,"response_timeout":-1}`,
			`{"status_code":200,"response_timeout":2147483648}`,
			`{"status_code":200}`,
		} {
			request(t, router, http.MethodPost, callbackOverrides+firstNotification, "", invalid, 400, "")
		}
		request(t, router, http.MethodPost, "/callback", "", event(firstNotification), 503, "")
		request(t, router, http.MethodDelete, callbackOverrides+firstNotification, "", "", 200, "")
		request(t, router, http.MethodDelete, callbackOverrides+firstNotification, "", "", 200, "")
		start := time.Now()
		request(t, router, http.MethodPost, "/callback", "", event(firstNotification), 200, "")
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("callback delay after reset = %v, want zero", elapsed)
		}
	})
}

func TestCallbackStopsWaitingWhenCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router, err := NewRouter()
		if err != nil {
			t.Fatal(err)
		}
		override := `{"status_code":503,"response_timeout":1000}`
		request(t, router, http.MethodPost, callbackOverrides+firstNotification, "", override, 200, "")

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/callback", strings.NewReader(event(firstNotification)))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			router.ServeHTTP(recorder, req)
		}()
		synctest.Wait()

		start := time.Now()
		cancel()
		<-done
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("callback cancellation took %v, want zero", elapsed)
		}
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("canceled callback status = %d, want 503", recorder.Code)
		}
	})
}

func TestOverrideRequestHeaders(t *testing.T) {
	router, err := NewRouter()
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"parameter":"price","value":19990}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, clientOverrides+"alice", strings.NewReader(body))
	req.Header.Set("X-Request-ID", "test-request")
	req.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported media type status = %d, want 415", recorder.Code)
	}

	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, clientOverrides+"alice", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing request ID status = %d, want 400", recorder.Code)
	}
	request(t, router, http.MethodGet, "/price", "alice", "", 200, `{"price":0}`)
}

func TestConcurrentClientOverrides(t *testing.T) {
	router, err := NewRouter()
	if err != nil {
		t.Fatal(err)
	}
	var clients sync.WaitGroup
	start := make(chan struct{})
	for id := range 8 {
		clients.Go(func() {
			<-start
			clientID := fmt.Sprintf("client-%d", id)
			path := clientOverrides + clientID
			body := fmt.Sprintf(`{"parameter":"price","value":%d}`, id+1)
			response := fmt.Sprintf(`{"price":%d}`, id+1)
			for range 10 {
				request(t, router, http.MethodPost, path, "", body, 200, "")
				request(t, router, http.MethodGet, "/price", clientID, "", 200, response)
				request(t, router, http.MethodDelete, path, "", "", 200, "")
				request(t, router, http.MethodGet, "/price", clientID, "", 200, `{"price":0}`)
			}
		})
	}
	close(start)
	clients.Wait()
}

func request(t *testing.T, router http.Handler, method, path, clientID, body string, status int, response string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if path != "/callback" {
		req.Header.Set("X-Request-ID", "test-request")
	}
	if clientID != "" {
		req.Header.Set("X-Client-ID", clientID)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != status {
		t.Errorf("%s %s status = %d, want %d, body = %s", method, path, recorder.Code, status, recorder.Body.String())
		return
	}
	if path != "/callback" && recorder.Header().Get("X-Request-ID") != "test-request" {
		t.Errorf("%s %s returned no request ID", method, path)
	}
	if status >= 400 {
		if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
			t.Errorf("%s %s content type = %q, want problem+json", method, path, got)
		}
		if expected := fmt.Sprintf(`"status":%d`, status); !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("%s %s problem body = %s, want %s", method, path, recorder.Body.String(), expected)
		}
	} else if got := strings.TrimSpace(recorder.Body.String()); got != response {
		t.Errorf("%s %s body = %s, want %s", method, path, got, response)
	}
}

func event(id string) string {
	return fmt.Sprintf(`{"notification_id":%q,"external_id":"test","notification_status":"success","sent_dt":"2026-12-01T09:00:01Z"}`, id)
}

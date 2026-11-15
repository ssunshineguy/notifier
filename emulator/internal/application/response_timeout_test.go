package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestServerPreservesDelayedCallbacks(t *testing.T) {
	baseURL := runTimeoutTest(t)
	client := &http.Client{Timeout: 1500*time.Millisecond + responseWriteTimeout + time.Second}
	t.Cleanup(client.CloseIdleConnections)

	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			override := fmt.Sprintf(`{"status_code":%d,"response_timeout":1500}`, status)
			configureCallbackHTTP(t, client, baseURL, override)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/callback", strings.NewReader(event(firstNotification)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			start := time.Now()
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != status {
				t.Fatalf("callback status = %d, want %d", response.StatusCode, status)
			}
			if elapsed := time.Since(start); elapsed < 1500*time.Millisecond || elapsed > 3500*time.Millisecond {
				t.Errorf("callback elapsed = %v, want 1.5s with a 2s transport margin", elapsed)
			}
			if status == http.StatusServiceUnavailable &&
				(response.Header.Get("Content-Type") != "application/problem+json" || !bytes.Contains(body, []byte(`"status":503`))) {
				t.Fatalf("callback problem = %s %s", response.Header.Get("Content-Type"), body)
			}
		})
	}
}

func TestMaximumCallbackDelayRemainsCancelable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router, err := NewRouter()
		if err != nil {
			t.Fatal(err)
		}
		request(t, router, http.MethodPost, callbackOverrides+firstNotification, "", `{"status_code":201,"response_timeout":2147483647}`, 200, "")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		handler := responseTimeout(responseWriteTimeout, router)
		response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/callback", strings.NewReader(event(firstNotification)))
		req.Header.Set("Content-Type", "application/json")
		done := make(chan struct{})
		go func() {
			defer close(done)
			handler.ServeHTTP(response, req)
		}()
		synctest.Wait()
		time.Sleep(2 * responseWriteTimeout)
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("callback returned before its configured delay or cancellation")
		default:
		}
		if len(response.deadlines) != 0 {
			t.Fatal("response write budget started during the callback delay")
		}
		cancel()
		<-done
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("canceled callback status = %d, want 503", response.Code)
		}
		if len(response.deadlines) != 1 || !response.deadlines[0].Equal(time.Now().Add(responseWriteTimeout)) {
			t.Fatalf("response write deadlines = %v, want one fresh response budget", response.deadlines)
		}
	})
}

func TestResponseWriteBudgetIsNotRenewed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		start := time.Now()
		handler := responseTimeout(time.Second, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			time.Sleep(500 * time.Millisecond)
			if _, err := w.Write([]byte("ok")); err != nil {
				t.Error(err)
			}
		}))
		handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		if len(response.deadlines) != 1 || !response.deadlines[0].Equal(start.Add(time.Second)) {
			t.Fatalf("write deadlines = %v, want the first write's deadline", response.deadlines)
		}
	})
}

func TestImplicitResponseHasWriteBudget(t *testing.T) {
	response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler := responseTimeout(time.Second, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	start := time.Now()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if len(response.deadlines) != 1 || response.deadlines[0].Before(start.Add(time.Second)) {
		t.Fatalf("implicit response write deadlines = %v, want a response budget", response.deadlines)
	}
}

func TestServerBoundsSlowResponseReader(t *testing.T) {
	type result struct {
		elapsed time.Duration
		written int
		err     error
	}
	const bodySize = 16 * 1024 * 1024
	finished := make(chan result, 1)
	server := &http.Server{
		Addr:              "127.0.0.1:0",
		ReadHeaderTimeout: time.Second,
		Handler: responseTimeout(responseWriteTimeout, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			body := bytes.Repeat([]byte("x"), bodySize)
			start := time.Now()
			n, err := w.Write(body)
			finished <- result{elapsed: time.Since(start), written: n, err: err}
		})),
	}
	server.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		if err := conn.(*net.TCPConn).SetWriteBuffer(4096); err != nil {
			t.Errorf("set server write buffer: %v", err)
		}
		return ctx
	}
	baseURL := serveTimeoutTest(t, server)
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", strings.TrimPrefix(baseURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := conn.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		var netErr net.Error
		if !errors.As(result.err, &netErr) || !netErr.Timeout() || result.written == bodySize {
			t.Fatalf("slow response write = %d bytes, %v; want a partial write and timeout", result.written, result.err)
		}
		if result.elapsed < responseWriteTimeout || result.elapsed > responseWriteTimeout+2*time.Second {
			t.Fatalf("slow response write took %v, want %v with a 2s margin", result.elapsed, responseWriteTimeout)
		}
	case <-time.After(responseWriteTimeout + 3*time.Second):
		t.Fatal("slow reader retained the handler beyond its response write budget")
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

// SetWriteDeadline records when the response budget starts for test assertions.
func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func serveTimeoutTest(t *testing.T, server *http.Server) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve HTTP: %v", err)
		}
	})
	return "http://" + listener.Addr().String()
}

func configureCallbackHTTP(t *testing.T, client *http.Client, baseURL, override string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+callbackOverrides+firstNotification, strings.NewReader(override))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "callback-timeout-test")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("configure callback status = %d, want 200", response.StatusCode)
	}
}

func runTimeoutTest(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, addr) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run emulator: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("emulator did not stop within its shutdown budget")
		}
	})
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	startup, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	for {
		conn, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(startup, "tcp", addr)
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			return "http://" + addr
		}
		select {
		case <-startup.Done():
			t.Fatalf("emulator did not start: %v", startup.Err())
		case <-ticker.C:
		}
	}
}

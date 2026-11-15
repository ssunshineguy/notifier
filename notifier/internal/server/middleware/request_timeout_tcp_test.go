package middleware_test

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/notifier/internal/server/middleware"
)

func testRequestTimeoutTCP(t *testing.T, next http.Handler, request string) {
	t.Helper()
	const processing = 300 * time.Millisecond
	const write = 100 * time.Millisecond
	handler := middleware.LogAPIRequests(slog.New(slog.NewTextHandler(io.Discard, nil)), middleware.RequestTimeout(processing, write, next))
	server := httptest.NewUnstartedServer(handler)
	server.Config.WriteTimeout = processing + write
	server.Start()
	t.Cleanup(server.Close)
	conn := dialBudgetServer(t, server)
	start := time.Now()
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	var problem codegen.Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || problem.Type != "/problems/request-timeout" ||
		problem.Status != http.StatusServiceUnavailable || problem.RequestID.Or("") != "tcp-timeout" ||
		response.Header.Get("Content-Type") != "application/problem+json" || response.Header.Get("X-Request-ID") != "tcp-timeout" || !response.Close {
		t.Fatalf("unexpected timeout: status=%d headers=%v problem=%+v close=%t", response.StatusCode, response.Header, problem, response.Close)
	}
	if elapsed := time.Since(start); elapsed < processing-20*time.Millisecond || elapsed > processing+write+time.Second {
		t.Fatalf("timeout response took %v, want about %v plus write budget and scheduling margin", elapsed, processing)
	}
}

func TestResponseWriteBudgetStopsSlowReaderTCP(t *testing.T) {
	const write = 100 * time.Millisecond
	const responseSize = 8 * 1024 * 1024
	finished := make(chan time.Duration, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(make([]byte, responseSize)); err != nil {
			t.Error(err)
		}
	})
	budgeted := middleware.LogAPIRequests(slog.New(slog.NewTextHandler(io.Discard, nil)), middleware.RequestTimeout(300*time.Millisecond, write, next))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		budgeted.ServeHTTP(w, r)
		finished <- time.Since(start)
	}))
	server.Config.WriteTimeout = 300*time.Millisecond + write
	server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			if err := conn.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
				t.Error(err)
			}
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	conn := dialBudgetServer(t, server)
	if err := conn.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: local\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case elapsed := <-finished:
		if elapsed < write-20*time.Millisecond || elapsed > write+time.Second {
			t.Fatalf("blocked response write took %v, want write budget %v plus margin", elapsed, write)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow reader kept the response writer blocked")
	}
	count, err := io.Copy(io.Discard, conn)
	if err != nil {
		t.Fatalf("read closed connection: %v", err)
	}
	if count >= responseSize {
		t.Fatalf("slow reader received the complete response (%d bytes), expected bounded partial write", count)
	}
}

func dialBudgetServer(t *testing.T, server *httptest.Server) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

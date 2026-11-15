package application_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/application"
)

func TestRunAPIUsesConfiguredConnectionLimit(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := apiTestConfig()
	cfg.API.ListenAddr = listener.Addr().String()
	cfg.API.MaxConnections = 2
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = application.RunAPI(ctx, cfg)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(6 * time.Second):
			t.Error("RunAPI did not stop with a full connection limit")
		}
	})

	connectCtx, stopConnecting := context.WithTimeout(t.Context(), 2*time.Second)
	defer stopConnecting()
	var first net.Conn
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	for {
		first, err = (&net.Dialer{}).DialContext(connectCtx, "tcp", cfg.API.ListenAddr)
		if err == nil {
			break
		}
		select {
		case <-connectCtx.Done():
			t.Fatal("RunAPI did not accept connections")
		case <-done:
			t.Fatalf("RunAPI stopped before accepting connections: %v", runErr)
		case <-retry.C:
		}
	}
	trackAPIConnection(t, first)
	writeAPIConnectionRequest(t, first)
	readAPIConnectionResponse(t, first)

	second, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", cfg.API.ListenAddr)
	if err != nil {
		t.Fatal(err)
	}
	trackAPIConnection(t, second)
	writeAPIConnectionRequest(t, second)
	readAPIConnectionResponse(t, second)

	waiting, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", cfg.API.ListenAddr)
	if err != nil {
		t.Fatal(err)
	}
	trackAPIConnection(t, waiting)
	writeAPIConnectionRequest(t, waiting)
	if err := waiting.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = waiting.Read(make([]byte, 1))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("third connection read = %v, want waiting at configured cap 2", err)
	}

	probe, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:9080/health/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Do(probe)
	if err != nil {
		t.Fatalf("health probe at full API connection limit: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health probe status = %d, want 200", response.StatusCode)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waiting.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	readAPIConnectionResponse(t, waiting)
}

func trackAPIConnection(t *testing.T, conn net.Conn) {
	t.Helper()
	t.Cleanup(func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
}

func writeAPIConnectionRequest(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := io.WriteString(conn, "GET /missing HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
}

func readAPIConnectionResponse(t *testing.T, conn net.Conn) {
	t.Helper()
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound || response.Close {
		t.Fatalf("response status=%d close=%t, want reusable HTTP 404", response.StatusCode, response.Close)
	}
}

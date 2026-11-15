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

func TestRunAPIClosesIdleKeepAliveTCP(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := apiTestConfig()
	cfg.API.ListenAddr = listener.Addr().String()
	cfg.API.IdleTimeoutMS = 120
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { finished <- application.RunAPI(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(6 * time.Second):
			t.Error("API did not stop")
		}
	})
	connectCtx, stopConnecting := context.WithTimeout(t.Context(), 2*time.Second)
	defer stopConnecting()
	var conn net.Conn
	for {
		conn, err = (&net.Dialer{}).DialContext(connectCtx, "tcp", cfg.API.ListenAddr)
		if err == nil {
			break
		}
		select {
		case <-connectCtx.Done():
			t.Fatal("API did not accept connections before test deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	for range 2 {
		if _, err := io.WriteString(conn, "GET /missing HTTP/1.1\r\nHost: local\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNotFound || response.Close {
			t.Fatalf("expected reusable 404 response, got status=%d close=%t", response.StatusCode, response.Close)
		}
	}
	start := time.Now()
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("idle connection read = %v, want server close", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond || elapsed > time.Second {
		t.Fatalf("idle close took %v, want 120ms plus scheduling margin", elapsed)
	}
}

package health

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeCountsActiveAndIdleConnectionsWithoutLimitingProbes(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/held" {
			close(held)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	service := startConnectionLimitServer(t, 2, time.Minute, handler)
	idle := service.connect(t)
	writeLimitRequest(t, idle, "/idle")
	readLimitResponse(t, idle, http.StatusOK)
	waitLimitEvent(t, service.idle, "first connection idle")

	active := service.connect(t)
	writeLimitRequest(t, active, "/held")
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("active request did not enter the handler")
	}
	waiting := dialLimitConnection(t, service.address)
	writeLimitRequest(t, waiting, "/waiting")
	assertLimitConnectionWaiting(t, waiting)
	select {
	case <-service.accepted:
		t.Fatal("server accepted a third connection while one idle and one active filled the limit")
	default:
	}

	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for _, path := range []string{"/health/live", "/health/ready"} {
		assertListenerResponds(t.Context(), t, client, "http://"+service.probeAddress+path, http.StatusOK, service.done)
	}
	closeLimitConnection(t, idle)
	waitLimitEvent(t, service.accepted, "waiting connection accepted after idle close")
	readLimitResponse(t, waiting, http.StatusOK)
	waitLimitEvent(t, service.idle, "waiting connection idle")

	close(release)
	readLimitResponse(t, active, http.StatusOK)
	waitLimitEvent(t, service.idle, "active connection idle")
	service.stop(t)
}

func TestServeReleasesConnectionSlotAfterIdleTimeout(t *testing.T) {
	const idleTimeout = 200 * time.Millisecond
	service := startConnectionLimitServer(t, 1, idleTimeout, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	idle := service.connect(t)
	writeLimitRequest(t, idle, "/idle")
	readLimitResponse(t, idle, http.StatusOK)
	waitLimitEvent(t, service.idle, "first connection idle")
	start := time.Now()
	waiting := dialLimitConnection(t, service.address)
	writeLimitRequest(t, waiting, "/waiting")
	assertLimitConnectionWaiting(t, waiting)
	readLimitResponse(t, waiting, http.StatusOK)
	waitLimitEvent(t, service.accepted, "waiting connection accepted after idle expiry")
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond || elapsed > time.Second {
		t.Fatalf("connection slot released after %v, want idle timeout %v plus margin", elapsed, idleTimeout)
	}
	if _, err := idle.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("expired idle connection read = %v, want EOF", err)
	}
}

type connectionLimitServer struct {
	address      string
	probeAddress string
	accepted     <-chan struct{}
	idle         <-chan struct{}
	done         <-chan struct{}
	cancel       context.CancelFunc
	serveErr     *error
}

func startConnectionLimitServer(t *testing.T, limit int, idleTimeout time.Duration, handler http.Handler) *connectionLimitServer {
	t.Helper()
	address, probeAddress := reserveLoopbackAddresses(t)
	_, probePort, err := net.SplitHostPort(probeAddress)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	accepted := make(chan struct{}, 8)
	idle := make(chan struct{}, 8)
	server := &http.Server{
		Addr: address, Handler: handler, ReadHeaderTimeout: time.Second, IdleTimeout: idleTimeout,
		BaseContext: func(net.Listener) context.Context { return ctx },
		ConnState: func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				accepted <- struct{}{}
			case http.StateIdle:
				idle <- struct{}{}
			}
		},
	}
	done := make(chan struct{})
	var serveErr error
	service := &connectionLimitServer{
		address: address, probeAddress: probeAddress, accepted: accepted, idle: idle,
		done: done, cancel: cancel, serveErr: &serveErr,
	}
	go func() {
		defer close(done)
		serveErr = Serve(ctx, server, probePort, NewHandler(func(context.Context) error { return nil }), limit)
	}()
	t.Cleanup(func() { service.stop(t) })
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	readyCtx, stopWaiting := context.WithTimeout(t.Context(), 2*time.Second)
	defer stopWaiting()
	assertListenerResponds(readyCtx, t, client, "http://"+probeAddress+"/health/live", http.StatusOK, done)
	return service
}

func (s *connectionLimitServer) connect(t *testing.T) net.Conn {
	t.Helper()
	conn := dialLimitConnection(t, s.address)
	waitLimitEvent(t, s.accepted, "connection accepted")
	return conn
}

func (s *connectionLimitServer) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	select {
	case <-s.done:
		if *s.serveErr != nil {
			t.Errorf("Serve shutdown = %v", *s.serveErr)
		}
	case <-time.After(time.Second):
		t.Error("Serve shutdown blocked with the connection limit full")
	}
}

func waitLimitEvent(t *testing.T, events <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for " + description)
	}
}

func dialLimitConnection(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeLimitConnection(t, conn) })
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func closeLimitConnection(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}

func writeLimitRequest(t *testing.T, conn net.Conn, path string) {
	t.Helper()
	if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
}

func readLimitResponse(t *testing.T, conn net.Conn, status int) {
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
	if response.StatusCode != status || response.Close {
		t.Fatalf("response status=%d close=%t, want reusable HTTP %d", response.StatusCode, response.Close, status)
	}
}

func assertLimitConnectionWaiting(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err := conn.Read(make([]byte, 1))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("over-limit response read = %v, want waiting for a connection slot", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

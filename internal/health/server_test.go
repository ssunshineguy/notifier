package health

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"
)

func TestServeStartsAndStopsBothListeners(t *testing.T) {
	serviceAddr, probeAddr := reserveLoopbackAddresses(t)
	_, probePort, err := net.SplitHostPort(probeAddr)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Addr: serviceAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: time.Second,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		serveErr = Serve(ctx, server, probePort, NewHandler(nil))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("Serve did not stop during cleanup")
		}
	})

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	probeCtx, stopProbing := context.WithTimeout(t.Context(), 5*time.Second)
	defer stopProbing()
	assertListenerResponds(probeCtx, t, client, "http://"+serviceAddr+"/", http.StatusNoContent, done)
	assertListenerResponds(probeCtx, t, client, "http://"+probeAddr+"/health/ready", http.StatusOK, done)

	cancel()
	select {
	case <-done:
		if serveErr != nil {
			t.Fatalf("Serve = %v after cancellation, want nil", serveErr)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Serve did not stop after cancellation")
	}

	var config net.ListenConfig
	for _, addr := range []string{serviceAddr, probeAddr} {
		listener, err := config.Listen(t.Context(), "tcp", addr)
		if err != nil {
			t.Errorf("reopen %s after shutdown: %v", addr, err)
			continue
		}
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}
}

func reserveLoopbackAddresses(t *testing.T) (string, string) {
	t.Helper()
	var config net.ListenConfig
	var addresses [2]string
	for i := range addresses {
		listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := listener.Close(); err != nil {
				t.Error(err)
			}
		}()
		addresses[i] = listener.Addr().String()
	}
	return addresses[0], addresses[1]
}

func assertListenerResponds(ctx context.Context, t *testing.T, client *http.Client, url string, wantStatus int, done <-chan struct{}) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	for {
		response, err := client.Do(request)
		if err == nil {
			if err := response.Body.Close(); err != nil {
				t.Errorf("close response from %s: %v", url, err)
			}
			if response.StatusCode != wantStatus {
				t.Fatalf("GET %s = %d, want %d", url, response.StatusCode, wantStatus)
			}
			return
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Fatalf("GET %s: %v", url, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("GET %s: listener did not become ready: %v", url, ctx.Err())
		case <-done:
			t.Fatalf("GET %s: server stopped before it responded", url)
		case <-retry.C:
		}
	}
}

func TestServeClosesServiceListenerIfProbePortIsOccupied(t *testing.T) {
	var config net.ListenConfig
	occupied, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := occupied.Close(); err != nil {
			t.Error(err)
		}
	})
	_, probePort, err := net.SplitHostPort(occupied.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	service, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := service.Addr().String()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: addr, ReadHeaderTimeout: time.Second}
	err = Serve(t.Context(), server, probePort, NewHandler(nil))
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Serve = %v, want address in use", err)
	}
	reopened, err := config.Listen(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("reopen service listener: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Error(err)
	}
}

func TestRunWithProbesStartsAndStopsBackgroundService(t *testing.T) {
	_, probeAddr := reserveLoopbackAddresses(t)
	_, probePort, err := net.SplitHostPort(probeAddr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	workerStarted := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		probes := NewHandler(func(context.Context) error { return errors.New("database unavailable") })
		runErr = RunWithProbes(ctx, probePort, probes, func(ctx context.Context) {
			close(workerStarted)
			<-ctx.Done()
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("RunWithProbes did not stop during cleanup")
		}
	})

	probeCtx, stopProbing := context.WithTimeout(t.Context(), 5*time.Second)
	defer stopProbing()
	select {
	case <-workerStarted:
	case <-done:
		t.Fatalf("background service failed to start: %v", runErr)
	case <-probeCtx.Done():
		t.Fatal("background service did not start")
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	assertListenerResponds(probeCtx, t, client, "http://"+probeAddr+"/health/live", http.StatusOK, done)
	assertListenerResponds(probeCtx, t, client, "http://"+probeAddr+"/health/ready", http.StatusServiceUnavailable, done)

	cancel()
	select {
	case <-done:
		if runErr != nil {
			t.Fatalf("RunWithProbes = %v after cancellation, want nil", runErr)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("RunWithProbes did not stop after cancellation")
	}
	reopened, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", probeAddr)
	if err != nil {
		t.Fatalf("reopen probe listener: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Error(err)
	}
}

func TestRunWithProbesFailsIfBackgroundServiceExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := RunWithProbes(ctx, "0", NewHandler(nil), func(context.Context) {})
	if err == nil || err.Error() != "background service stopped unexpectedly" {
		t.Fatalf("RunWithProbes = %v, want an error for unexpected service exit", err)
	}
}

package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/netutil"
)

// Serve limits public connections when maxConnections is positive; probes keep a separate listener.
func Serve(ctx context.Context, server *http.Server, probePort string, probes http.Handler, maxConnections int) error {
	if ctx.Err() != nil {
		return nil
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return err
	}
	if maxConnections > 0 {
		listener = netutil.LimitListener(listener, maxConnections)
	}
	probeAddr := net.JoinHostPort("127.0.0.1", probePort)
	probeListener, err := config.Listen(ctx, "tcp", probeAddr)
	if err != nil {
		return errors.Join(fmt.Errorf("listen for health probes: %w", err), listener.Close())
	}

	probeServer := &http.Server{
		Handler:           probes,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 2)
	var serving sync.WaitGroup
	serving.Go(func() {
		serveErr <- server.Serve(listener)
	})
	serving.Go(func() {
		serveErr <- fmt.Errorf("serve health probes: %w", probeServer.Serve(probeListener))
	})

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serveErr:
		if errors.Is(runErr, http.ErrServerClosed) {
			runErr = nil
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	probeShutdown := make(chan error, 1)
	go func() {
		probeShutdown <- shutdown(shutdownCtx, probeServer)
	}()
	err = errors.Join(runErr, shutdown(shutdownCtx, server), <-probeShutdown)
	serving.Wait()
	return err
}

// RunWithProbes runs a background service with a loopback-only probe listener.
func RunWithProbes(ctx context.Context, probePort string, probes http.Handler, run func(context.Context)) error {
	if ctx.Err() != nil {
		return nil
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", probePort))
	if err != nil {
		return fmt.Errorf("listen for health probes: %w", err)
	}

	probeServer := &http.Server{
		Handler:           probes,
		ReadHeaderTimeout: 5 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	failures := make(chan error, 2)
	var running sync.WaitGroup
	running.Go(func() {
		if err := probeServer.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			failures <- fmt.Errorf("serve health probes: %w", err)
		}
	})
	running.Go(func() {
		run(runCtx)
		if runCtx.Err() == nil {
			failures <- errors.New("background service stopped unexpectedly")
		}
	})

	select {
	case <-ctx.Done():
	case err = <-failures:
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	err = errors.Join(err, shutdown(shutdownCtx, probeServer))
	running.Wait()
	return err
}

func shutdown(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", errors.Join(err, server.Close()))
	}
	return nil
}

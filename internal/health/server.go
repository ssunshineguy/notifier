package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Serve runs the service and loopback probes with a shared shutdown budget.
func Serve(ctx context.Context, server *http.Server, probePort string, probes http.Handler) error {
	if ctx.Err() != nil {
		return nil
	}
	var config net.ListenConfig
	listener, err := config.Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return err
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

func shutdown(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", errors.Join(err, server.Close()))
	}
	return nil
}

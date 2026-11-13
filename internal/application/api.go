package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/internal/config"
	"github.com/ssunshineguy/notifier/internal/server/codegen"
	"github.com/ssunshineguy/notifier/internal/server/handlers"
	"github.com/ssunshineguy/notifier/internal/server/middleware"
	"github.com/ssunshineguy/notifier/internal/storage/postgres"
)

// RunAPI serves requests on the configured address until ctx is canceled.
func RunAPI(ctx context.Context, cfg config.Config) error {
	poolConfig, err := pgxpool.ParseConfig(cfg.Database.APIDSN)
	if err != nil {
		return errors.New("invalid API database configuration")
	}
	poolConfig.MinConns = cfg.Database.MinConns
	poolConfig.MaxConns = cfg.Database.MaxConns
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open API database pool: %w", err)
	}
	defer pool.Close()

	operationTimeout := time.Duration(cfg.Database.OperationTimeoutSec) * time.Second
	store := postgres.NewStore(pool, operationTimeout)
	scheduledTime := time.Duration(cfg.Notification.ScheduledTimeSec) * time.Second
	router, err := codegen.NewServer(
		handlers.NewHandler(store, scheduledTime),
		middleware.NewClientSecurity(store),
		codegen.WithErrorHandler(middleware.HandleError),
	)
	if err != nil {
		return fmt.Errorf("create API router: %w", err)
	}

	slotWaitTimeout := time.Duration(cfg.API.SlotWaitTimeoutMS) * time.Millisecond
	limited := middleware.LimitInFlight(cfg.API.MaxInFlight, cfg.API.MaxQueued, slotWaitTimeout, router)
	requestTimeout := time.Duration(cfg.API.RequestTimeoutMS) * time.Millisecond
	server := &http.Server{
		Addr:              cfg.API.ListenAddr,
		Handler:           middleware.RequestTimeout(requestTimeout, limited),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "starting API server", "address", server.Addr)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve API: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down API: %w", errors.Join(err, server.Close()))
		}
		return nil
	}
}

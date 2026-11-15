package application_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/application"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestRunAPIRejectsInvalidDSN(t *testing.T) {
	cfg := apiTestConfig()
	cfg.Database.DSN = "postgres://localhost:invalid/notifier"
	err := application.RunAPI(t.Context(), cfg)
	if err == nil || err.Error() != "configure api database: invalid database connection string" {
		t.Fatalf("RunAPI = %v, want invalid database configuration", err)
	}
}

func TestRunAPIReportsOccupiedAddress(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})

	cfg := apiTestConfig()
	cfg.API.ListenAddr = listener.Addr().String()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = application.RunAPI(ctx, cfg)
	if !errors.Is(err, syscall.EADDRINUSE) || !strings.HasPrefix(err.Error(), "serve API:") {
		t.Fatalf("RunAPI = %v, want an address-in-use error from the HTTP server", err)
	}
}

func TestRunAPIWithAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := application.RunAPI(ctx, apiTestConfig()); err != nil {
		t.Fatalf("RunAPI with a canceled context = %v, want nil", err)
	}
}

func apiTestConfig() config.Config {
	return config.Config{
		API: config.API{
			ListenAddr: "127.0.0.1:0", MaxInFlight: 1, MaxConnections: 1024, RequestTimeoutMS: 300, WriteTimeoutMS: 1000, IdleTimeoutMS: 60000,
		},
		Database: config.Database{
			DSN: "postgres://127.0.0.1:5432/notifier?sslmode=disable", MaxConns: 1,
			OperationTimeoutSec: 1,
		},
		Notification: config.Notification{ScheduledTimeSec: 1},
	}
}

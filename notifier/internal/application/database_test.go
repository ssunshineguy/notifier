package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestDatabasePoolPreservesSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := config.Database{
		DSN:      "postgres://worker@127.0.0.1:1/notifier?sslmode=disable&application_name=pool-test&connect_timeout=3&pool_max_conns=9&pool_min_conns=0&pool_max_conn_lifetime=5m",
		MinConns: 1, MaxConns: 3,
	}
	pool, err := newDatabasePool(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	got := pool.Config()
	if got.MinConns != 1 || got.MaxConns != 3 || got.MaxConnLifetime != 5*time.Minute {
		t.Fatalf("pool limits = min %d max %d lifetime %v", got.MinConns, got.MaxConns, got.MaxConnLifetime)
	}
	if got.ConnConfig.User != "worker" || got.ConnConfig.Database != "notifier" || got.ConnConfig.Port != 1 ||
		got.ConnConfig.RuntimeParams["application_name"] != "pool-test" || got.ConnConfig.ConnectTimeout != 3*time.Second {
		t.Fatal("pool did not preserve connection settings from the selected DSN")
	}
}

func TestModeWiringRejectsMissingAndInvalidDSN(t *testing.T) {
	for name, run := range map[string]func(context.Context, config.Config) error{
		"api": RunAPI,
		"worker": func(ctx context.Context, cfg config.Config) error {
			return RunWorker(ctx, cfg, func() {})
		},
		"migrate": Migrate,
	} {
		for _, dsn := range []string{"", " \t\n", "postgres://role:synthetic-secret@localhost:invalid/notifier"} {
			t.Run(name, func(t *testing.T) {
				err := run(t.Context(), config.Config{Database: config.Database{DSN: dsn}})
				if err == nil {
					t.Fatal("mode accepted a missing or invalid DSN")
				}
				if strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatal("mode error contains the DSN password")
				}
			})
		}
	}
}

func TestRunWorkerWithOnlyItsDSN(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := config.Config{
		Database:     config.Database{DSN: "postgres://worker@127.0.0.1:1/notifier?sslmode=disable", MaxConns: 1, OperationTimeoutSec: 1},
		InfoService:  config.InfoService{BaseURL: "http://127.0.0.1:1", TimeTimeoutMS: 100, PriceTimeoutMS: 100},
		Worker:       config.Worker{Workers: 1, BatchSize: 1, CycleTimeoutMS: 100},
		Notification: config.Notification{ScheduledTimeSec: 1, LockDurationMS: 100},
	}
	if err := RunWorker(ctx, cfg, cancel); err != nil {
		t.Fatalf("RunWorker with a canceled context = %v, want nil", err)
	}
}

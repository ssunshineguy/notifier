package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

const validYAML = `database:
notification:
  scheduled_time_sec: 10
`

func TestLoadDefaults(t *testing.T) {
	got, err := config.Load(writeConfig(t, validYAML), "api", "")
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		API: config.API{
			ListenAddr: "127.0.0.1:8080", MaxInFlight: 16, MaxConnections: 1024, MaxQueued: 16,
			SlotWaitTimeoutMS: 100, RequestTimeoutMS: 300, WriteTimeoutMS: 1000, IdleTimeoutMS: 60000,
		},
		Worker:      config.Worker{Workers: 2, BatchSize: 8, CycleTimeoutMS: 2000},
		InfoService: config.InfoService{BaseURL: "http://emulator:8081", TimeTimeoutMS: 200, PriceTimeoutMS: 200},
		Database: config.Database{
			MaxConns: 8, OperationTimeoutSec: 1,
			DSN: "postgres://api@localhost/notifier",
		},
		Notification: config.Notification{InitialCheckDelaySec: 0, ScheduledTimeSec: 10, LockDurationMS: 2000},
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadExplicitSettings(t *testing.T) {
	yaml := strings.Replace(validYAML, "database:\n", "database:\n  min_conns: 3\n  max_conns: 3\n  operation_timeout_sec: 2\n", 1)
	yaml = strings.Replace(yaml, "scheduled_time_sec: 10", "scheduled_time_sec: 12\n  lock_duration_ms: 4250", 1)
	yaml += `api:
  listen_addr: 0.0.0.0:8081
  max_in_flight: 4
  max_queued: 0
  slot_wait_timeout_ms: 0
  request_timeout_ms: 750
worker:
  workers: 3
  batch_size: 5
  cycle_timeout_ms: 1500
infoservice:
  base_url: https://infoservice.example
  time_timeout_ms: 150
  price_timeout_ms: 350
`
	got, err := config.Load(writeConfig(t, yaml), "api", "")
	if err != nil {
		t.Fatal(err)
	}
	wantAPI := config.API{ListenAddr: "0.0.0.0:8081", MaxInFlight: 4, MaxConnections: 1024, RequestTimeoutMS: 750, WriteTimeoutMS: 1000, IdleTimeoutMS: 60000}
	if got.API != wantAPI {
		t.Errorf("API = %+v, want %+v", got.API, wantAPI)
	}
	wantWorker := config.Worker{Workers: 3, BatchSize: 5, CycleTimeoutMS: 1500}
	if got.Worker != wantWorker {
		t.Errorf("Worker = %+v, want %+v", got.Worker, wantWorker)
	}
	wantInfoService := config.InfoService{BaseURL: "https://infoservice.example", TimeTimeoutMS: 150, PriceTimeoutMS: 350}
	if got.InfoService != wantInfoService {
		t.Errorf("InfoService = %+v, want %+v", got.InfoService, wantInfoService)
	}
	wantNotification := config.Notification{InitialCheckDelaySec: 0, ScheduledTimeSec: 12, LockDurationMS: 4250}
	if got.Notification != wantNotification {
		t.Errorf("Notification = %+v, want %+v", got.Notification, wantNotification)
	}
	if got.Database.MinConns != 3 || got.Database.MaxConns != 3 || got.Database.OperationTimeoutSec != 2 {
		t.Errorf("unexpected pool settings: min=%d max=%d timeout=%d", got.Database.MinConns, got.Database.MaxConns, got.Database.OperationTimeoutSec)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{"unknown field", "scheduled_time_sec:", "scheduled_time_seconds:", "field scheduled_time_seconds not found"},
		{"malformed YAML", "scheduled_time_sec: 10", "scheduled_time_sec: [", "parse config"},
		{"empty listen address", "notification:\n", "api:\n  listen_addr: ''\nnotification:\n", "api.listen_addr is required"},
		{"invalid scheduling interval", "scheduled_time_sec: 10", "scheduled_time_sec: 0", "notification.scheduled_time_sec must be positive"},
		{"zero pool limit", "database:\n", "database:\n  max_conns: 0\n", "database.max_conns must be positive"},
		{"zero SQL timeout", "database:\n", "database:\n  operation_timeout_sec: 0\n", "database.operation_timeout_sec must be positive"},
		{"pool minimum above limit", "database:\n", "database:\n  min_conns: 9\n", "database.min_conns must be between 0 and database.max_conns"},
		{"negative pool minimum", "database:\n", "database:\n  min_conns: -1\n", "database.min_conns must be between 0 and database.max_conns"},
		{"zero concurrency", "notification:\n", "api:\n  max_in_flight: 0\nnotification:\n", "api.max_in_flight must be positive"},
		{"zero request timeout", "notification:\n", "api:\n  request_timeout_ms: 0\nnotification:\n", "api.request_timeout_ms must be positive"},
		{"negative queue size", "notification:\n", "api:\n  max_queued: -1\nnotification:\n", "api.max_queued must be nonnegative"},
		{"zero worker count", "notification:\n", "worker:\n  workers: 0\nnotification:\n", "worker.workers must be positive"},
		{"zero worker batch size", "notification:\n", "worker:\n  batch_size: 0\nnotification:\n", "worker.batch_size must be positive"},
		{"zero worker cycle timeout", "notification:\n", "worker:\n  cycle_timeout_ms: 0\nnotification:\n", "worker.cycle_timeout_ms must be positive"},
		{"empty information service URL", "notification:\n", "infoservice:\n  base_url: ''\nnotification:\n", "infoservice.base_url must be an absolute HTTP or HTTPS URL"},
		{"relative information service URL", "notification:\n", "infoservice:\n  base_url: /emulator\nnotification:\n", "infoservice.base_url must be an absolute HTTP or HTTPS URL"},
		{"unsupported information service URL scheme", "notification:\n", "infoservice:\n  base_url: ftp://emulator\nnotification:\n", "infoservice.base_url must be an absolute HTTP or HTTPS URL"},
		{"invalid information service URL", "notification:\n", "infoservice:\n  base_url: http://emulator:bad\nnotification:\n", "infoservice.base_url must be an absolute HTTP or HTTPS URL"},
		{"zero time timeout", "notification:\n", "infoservice:\n  time_timeout_ms: 0\nnotification:\n", "infoservice.time_timeout_ms must be positive"},
		{"negative price timeout", "notification:\n", "infoservice:\n  price_timeout_ms: -1\nnotification:\n", "infoservice.price_timeout_ms must be positive"},
		{"zero reservation duration", "notification:\n", "notification:\n  lock_duration_ms: 0\n", "notification.lock_duration_ms must be positive"},
		{"reservation shorter than cycle timeout", "notification:\n", "notification:\n  lock_duration_ms: 1500\n", "notification.lock_duration_ms must be at least worker.cycle_timeout_ms"},
		{"negative queue timeout", "notification:\n", "api:\n  slot_wait_timeout_ms: -1\nnotification:\n", "api.slot_wait_timeout_ms must be nonnegative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(validYAML, tt.old, tt.new, 1)
			_, err := config.Load(writeConfig(t, yaml), "api", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"), "api", "")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load = %v, want os.ErrNotExist", err)
	}
}

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "api.dsn"), []byte("postgres://api@localhost/notifier"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

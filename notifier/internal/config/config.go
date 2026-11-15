// Package config loads the service configuration from a YAML file.
package config

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config mirrors notifier/configs/config.yaml.
type Config struct {
	API          API          `yaml:"api"`
	Worker       Worker       `yaml:"worker"`
	InfoService  InfoService  `yaml:"infoservice"`
	Database     Database     `yaml:"database"`
	Notification Notification `yaml:"notification"`
}

// API configures the HTTP listener and its request concurrency limit.
type API struct {
	ListenAddr        string `yaml:"listen_addr"`
	MaxInFlight       int    `yaml:"max_in_flight"`
	MaxQueued         int    `yaml:"max_queued"`
	SlotWaitTimeoutMS int32  `yaml:"slot_wait_timeout_ms"`
	RequestTimeoutMS  int32  `yaml:"request_timeout_ms"`
}

// Worker sets polling and processing limits.
type Worker struct {
	Workers        int   `yaml:"workers"`
	BatchSize      int   `yaml:"batch_size"`
	CycleTimeoutMS int32 `yaml:"cycle_timeout_ms"`
}

// InfoService sets the service address and request budgets.
type InfoService struct {
	BaseURL        string `yaml:"base_url"`
	TimeTimeoutMS  int32  `yaml:"time_timeout_ms"`
	PriceTimeoutMS int32  `yaml:"price_timeout_ms"`
}

// Database configures role-specific DSNs and the connection pool.
type Database struct {
	MinConns            int32  `yaml:"min_conns"`
	MaxConns            int32  `yaml:"max_conns"`
	OperationTimeoutSec int32  `yaml:"operation_timeout_sec"`
	MigratorDSN         string `yaml:"migrator_dsn"`
	APIDSN              string `yaml:"api_dsn"`
	WorkerDSN           string `yaml:"worker_dsn"`
	CollectorDSN        string `yaml:"collector_dsn"`
}

// InitialCheckDelaySeconds decodes the first-check delay as whole seconds.
type InitialCheckDelaySeconds int64

// UnmarshalYAML rejects non-integer values instead of truncating fractional seconds.
func (d *InitialCheckDelaySeconds) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag != "!!int" {
		return fmt.Errorf("notification.initial_check_delay_sec must be an integer number of seconds")
	}
	var seconds int64
	if err := value.Decode(&seconds); err != nil {
		return err
	}
	*d = InitialCheckDelaySeconds(seconds)
	return nil
}

// Notification holds the notification rules shared by the api and worker modes.
type Notification struct {
	InitialCheckDelaySec InitialCheckDelaySeconds `yaml:"initial_check_delay_sec"`
	ScheduledTimeSec     int                      `yaml:"scheduled_time_sec"`
	LockDurationMS       int32                    `yaml:"lock_duration_ms"`
}

// Load reads and validates the config file at path.
// Unknown keys are rejected, so a typo in the file fails at startup.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The path comes from the operator's -config flag.
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	cfg := Config{
		API:          API{ListenAddr: "127.0.0.1:8080", MaxInFlight: 16, MaxQueued: 16, SlotWaitTimeoutMS: 100, RequestTimeoutMS: 2000},
		Worker:       Worker{Workers: 2, BatchSize: 8, CycleTimeoutMS: 2000},
		InfoService:  InfoService{BaseURL: "http://emulator:8081", TimeTimeoutMS: 200, PriceTimeoutMS: 200},
		Database:     Database{MaxConns: 8, OperationTimeoutSec: 1},
		Notification: Notification{InitialCheckDelaySec: 0, LockDurationMS: 2000},
	}
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	required := []struct{ key, value string }{
		{"database.migrator_dsn", cfg.Database.MigratorDSN},
		{"database.api_dsn", cfg.Database.APIDSN},
		{"api.listen_addr", cfg.API.ListenAddr},
		{"database.worker_dsn", cfg.Database.WorkerDSN},
		{"database.collector_dsn", cfg.Database.CollectorDSN},
	}
	for _, field := range required {
		if field.value == "" {
			return Config{}, fmt.Errorf("config: %s is required", field.key)
		}
	}

	baseURL, err := url.Parse(cfg.InfoService.BaseURL)
	if err != nil || baseURL.Hostname() == "" || (baseURL.Scheme != "http" && baseURL.Scheme != "https") {
		return Config{}, fmt.Errorf("config: infoservice.base_url must be an absolute HTTP or HTTPS URL with a host")
	}

	if cfg.Notification.InitialCheckDelaySec < 0 || cfg.Notification.InitialCheckDelaySec > InitialCheckDelaySeconds(math.MaxInt64/int64(time.Second)) {
		return Config{}, fmt.Errorf("config: notification.initial_check_delay_sec must be between 0 and %d", math.MaxInt64/int64(time.Second))
	}

	positive := []struct {
		key   string
		value int
	}{
		{"notification.scheduled_time_sec", cfg.Notification.ScheduledTimeSec},
		{"database.max_conns", int(cfg.Database.MaxConns)},
		{"database.operation_timeout_sec", int(cfg.Database.OperationTimeoutSec)},
		{"api.max_in_flight", cfg.API.MaxInFlight},
		{"api.request_timeout_ms", int(cfg.API.RequestTimeoutMS)},
		{"worker.workers", cfg.Worker.Workers},
		{"worker.batch_size", cfg.Worker.BatchSize},
		{"worker.cycle_timeout_ms", int(cfg.Worker.CycleTimeoutMS)},
		{"infoservice.time_timeout_ms", int(cfg.InfoService.TimeTimeoutMS)},
		{"infoservice.price_timeout_ms", int(cfg.InfoService.PriceTimeoutMS)},
		{"notification.lock_duration_ms", int(cfg.Notification.LockDurationMS)},
	}
	for _, field := range positive {
		if field.value <= 0 {
			return Config{}, fmt.Errorf("config: %s must be positive", field.key)
		}
	}

	if cfg.Notification.LockDurationMS < cfg.Worker.CycleTimeoutMS {
		return Config{}, fmt.Errorf("config: notification.lock_duration_ms must be at least worker.cycle_timeout_ms")
	}

	if cfg.Database.MinConns < 0 || cfg.Database.MinConns > cfg.Database.MaxConns {
		return Config{}, fmt.Errorf("config: database.min_conns must be between 0 and database.max_conns (%d)", cfg.Database.MaxConns)
	}

	if cfg.API.MaxQueued < 0 {
		return Config{}, fmt.Errorf("config: api.max_queued must be nonnegative")
	}
	if cfg.API.SlotWaitTimeoutMS < 0 {
		return Config{}, fmt.Errorf("config: api.slot_wait_timeout_ms must be nonnegative")
	}

	return cfg, nil
}

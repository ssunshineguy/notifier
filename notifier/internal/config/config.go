// Package config loads the service configuration from a YAML file.
package config

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
)

// Config mirrors notifier/configs/config.yaml.
type Config struct {
	API          API          `yaml:"api"`
	Worker       Worker       `yaml:"worker"`
	Collector    Collector    `yaml:"collector"`
	InfoService  InfoService  `yaml:"infoservice"`
	Database     Database     `yaml:"database"`
	Notification Notification `yaml:"notification"`
}

// API configures HTTP connection budgets and request admission.
type API struct {
	ListenAddr        string `yaml:"listen_addr"`
	MaxInFlight       int    `yaml:"max_in_flight"`
	MaxConnections    int    `yaml:"max_connections"`
	MaxQueued         int    `yaml:"max_queued"`
	SlotWaitTimeoutMS int32  `yaml:"slot_wait_timeout_ms"`
	RequestTimeoutMS  int32  `yaml:"request_timeout_ms"`
	WriteTimeoutMS    int32  `yaml:"write_timeout_ms"`
	IdleTimeoutMS     int32  `yaml:"idle_timeout_ms"`
}

// Worker sets polling and processing limits.
type Worker struct {
	Workers        int   `yaml:"workers"`
	BatchSize      int   `yaml:"batch_size"`
	CycleTimeoutMS int32 `yaml:"cycle_timeout_ms"`
}

// Collector sets retention and the maximum rows removed per transaction.
type Collector struct {
	BatchSize     CollectorBatchSize `yaml:"batch_size"`
	RetentionDays RetentionDays      `yaml:"retention_days"`
}

// CollectorBatchSize requires a whole number of rows per deletion transaction.
type CollectorBatchSize int

// UnmarshalYAML rejects fractional limits instead of silently reducing them.
func (b *CollectorBatchSize) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag != "!!int" {
		return fmt.Errorf("collector.batch_size must be an integer number of rows")
	}
	var count int
	if err := value.Decode(&count); err != nil {
		return err
	}
	*b = CollectorBatchSize(count)
	return nil
}

// RetentionDays rejects fractional retention values that could delete data too early.
type RetentionDays int32

// UnmarshalYAML requires a whole number of days rather than truncating a fractional value.
func (d *RetentionDays) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag != "!!int" {
		return fmt.Errorf("collector.retention_days must be an integer number of days")
	}
	var days int32
	if err := value.Decode(&days); err != nil {
		return err
	}
	*d = RetentionDays(days)
	return nil
}

// InfoService sets the service address and request budgets.
type InfoService struct {
	BaseURL        string `yaml:"base_url"`
	TimeTimeoutMS  int32  `yaml:"time_timeout_ms"`
	PriceTimeoutMS int32  `yaml:"price_timeout_ms"`
}

// Database holds the selected mode's DSN and shared connection pool settings.
type Database struct {
	MinConns            int32  `yaml:"min_conns"`
	MaxConns            int32  `yaml:"max_conns"`
	OperationTimeoutSec int32  `yaml:"operation_timeout_sec"`
	DSN                 string `yaml:"-"`
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
	InitialCheckDelaySec   InitialCheckDelaySeconds `yaml:"initial_check_delay_sec"`
	TechnicalRetryDelaySec int32                    `yaml:"technical_retry_delay_sec"`
	ScheduledTimeSec       int                      `yaml:"scheduled_time_sec"`
	LockDurationMS         int32                    `yaml:"lock_duration_ms"`
}

// Load reads shared settings and only the selected mode's DSN file.
// An empty dsnPath selects <mode>.dsn alongside path; unknown YAML keys are rejected.
func Load(path, mode, dsnPath string) (Config, error) {
	switch mode {
	case "api", "worker", "collector", "migrate":
	default:
		return Config{}, fmt.Errorf("unknown mode %q", mode)
	}
	data, err := os.ReadFile(path) //nolint:gosec // The path comes from the operator's -config flag.
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	cfg := Config{
		API:          API{ListenAddr: "127.0.0.1:8080", MaxInFlight: 16, MaxConnections: 1024, MaxQueued: 16, SlotWaitTimeoutMS: 100, RequestTimeoutMS: 300, WriteTimeoutMS: 1000, IdleTimeoutMS: 60000},
		Worker:       Worker{Workers: 2, BatchSize: 8, CycleTimeoutMS: 2000},
		Collector:    Collector{BatchSize: 1000, RetentionDays: 365},
		InfoService:  InfoService{BaseURL: "http://emulator:8081", TimeTimeoutMS: 200, PriceTimeoutMS: 200},
		Database:     Database{MaxConns: 8, OperationTimeoutSec: 1},
		Notification: Notification{InitialCheckDelaySec: 0, TechnicalRetryDelaySec: 10, LockDurationMS: 2000},
	}
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	required := []struct{ key, value string }{
		{"api.listen_addr", cfg.API.ListenAddr},
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
		{"notification.technical_retry_delay_sec", int(cfg.Notification.TechnicalRetryDelaySec)},
		{"database.max_conns", int(cfg.Database.MaxConns)},
		{"database.operation_timeout_sec", int(cfg.Database.OperationTimeoutSec)},
		{"api.max_in_flight", cfg.API.MaxInFlight},
		{"api.max_connections", cfg.API.MaxConnections},
		{"api.request_timeout_ms", int(cfg.API.RequestTimeoutMS)},
		{"api.write_timeout_ms", int(cfg.API.WriteTimeoutMS)},
		{"api.idle_timeout_ms", int(cfg.API.IdleTimeoutMS)},
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

	const maxRetentionDays = RetentionDays(math.MaxInt64 / int64(24*time.Hour))
	if cfg.Collector.RetentionDays < 1 || cfg.Collector.RetentionDays > maxRetentionDays {
		return Config{}, fmt.Errorf("config: collector.retention_days must be between 1 and %d", maxRetentionDays)
	}

	if cfg.Collector.BatchSize < 1 {
		return Config{}, fmt.Errorf("config: collector.batch_size must be positive")
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

	if dsnPath == "" {
		dsnPath = filepath.Join(filepath.Dir(path), mode+".dsn")
	}
	dsn, err := os.ReadFile(dsnPath) //nolint:gosec // The path comes from the operator's config or -database-dsn-file flag.
	if err != nil {
		return Config{}, &DatabaseConfigError{mode: mode, reason: "cannot read DSN file; provide -database-dsn-file"}
	}
	cfg.Database.DSN = strings.TrimSpace(string(dsn))
	if cfg.Database.DSN == "" {
		return Config{}, &DatabaseConfigError{mode: mode, reason: "DSN file must not be empty"}
	}
	if _, err := pgxpool.ParseConfig(cfg.Database.DSN); err != nil {
		return Config{}, &DatabaseConfigError{mode: mode, reason: "DSN file contains an invalid connection string"}
	}
	return cfg, nil
}

// DatabaseConfigError contains only a validated mode and a safe startup diagnostic.
type DatabaseConfigError struct {
	mode   string
	reason string
}

// Error excludes the DSN, file path and raw driver errors so it can be logged safely.
func (e *DatabaseConfigError) Error() string {
	return fmt.Sprintf("config: %s database: %s", e.mode, e.reason)
}

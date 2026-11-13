// Package config loads the service configuration from a YAML file.
package config

import (
	"bytes"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Config mirrors configs/config.yaml.
type Config struct {
	Database Database `yaml:"database"`
}

// Database holds one DSN per database role.
type Database struct {
	MigratorDSN  string `yaml:"migrator_dsn"`
	APIDSN       string `yaml:"api_dsn"`
	WorkerDSN    string `yaml:"worker_dsn"`
	CollectorDSN string `yaml:"collector_dsn"`
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

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	required := []struct{ key, value string }{
		{"database.migrator_dsn", cfg.Database.MigratorDSN},
		{"database.api_dsn", cfg.Database.APIDSN},
		{"database.worker_dsn", cfg.Database.WorkerDSN},
		{"database.collector_dsn", cfg.Database.CollectorDSN},
	}
	for _, field := range required {
		if field.value == "" {
			return Config{}, fmt.Errorf("config: %s is required", field.key)
		}
	}

	return cfg, nil
}

package config_test

import (
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestLoadCollectorSettings(t *testing.T) {
	for _, tt := range []struct {
		name string
		yaml string
		want config.Collector
	}{
		{"default", "", config.Collector{BatchSize: 1000, RetentionDays: 365}},
		{"minimum", "collector:\n  batch_size: 1\n  retention_days: 1\n", config.Collector{BatchSize: 1, RetentionDays: 1}},
		{"maximum retention", "collector:\n  batch_size: 1000\n  retention_days: 106751\n", config.Collector{BatchSize: 1000, RetentionDays: 106751}},
		{"batch above default", "collector:\n  batch_size: 1001\n", config.Collector{BatchSize: 1001, RetentionDays: 365}},
		{"explicit retention", "collector:\n  retention_days: 400\n", config.Collector{BatchSize: 1000, RetentionDays: 400}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.Load(writeConfig(t, validYAML+tt.yaml), "api", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Collector != tt.want {
				t.Fatalf("collector settings = %+v, want %+v", got.Collector, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidCollectorSettings(t *testing.T) {
	for _, tt := range []struct {
		name string
		yaml string
		want string
	}{
		{"fractional batch", "batch_size: 1.9", "collector.batch_size must be an integer number of rows"},
		{"large fractional batch", "batch_size: 1000.9", "collector.batch_size must be an integer number of rows"},
		{"zero batch", "batch_size: 0", "collector.batch_size must be positive"},
		{"negative batch", "batch_size: -1", "collector.batch_size must be positive"},
		{"fractional retention", "retention_days: 1.5", "collector.retention_days must be an integer number of days"},
		{"quoted retention", "retention_days: '365'", "collector.retention_days must be an integer number of days"},
		{"zero retention", "retention_days: 0", "collector.retention_days must be between 1 and 106751"},
		{"negative retention", "retention_days: -1", "collector.retention_days must be between 1 and 106751"},
		{"retention duration overflow", "retention_days: 106752", "collector.retention_days must be between 1 and 106751"},
		{"obsolete goroutines", "goroutines: 2", "field goroutines not found"},
		{"obsolete cycle timeout", "cycle_timeout_ms: 2000", "field cycle_timeout_ms not found"},
		{"obsolete poll interval", "poll_interval_sec: 3600", "field poll_interval_sec not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, validYAML+"collector:\n  "+tt.yaml+"\n"), "api", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

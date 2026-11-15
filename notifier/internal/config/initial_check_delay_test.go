package config_test

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestLoadInitialCheckDelay(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		want    int64
	}{
		{"omitted", "", 0},
		{"zero", "0", 0},
		{"null uses default", "null", 0},
		{"hours", "7200", 7200},
		{"maximum duration", strconv.FormatInt(math.MaxInt64/int64(time.Second), 10), math.MaxInt64 / int64(time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(validYAML, "scheduled_time_sec: 10", "scheduled_time_sec: 37", 1)
			if tt.setting != "" {
				yaml = strings.Replace(yaml, "notification:\n", "notification:\n  initial_check_delay_sec: "+tt.setting+"\n", 1)
			}
			got, err := config.Load(writeConfig(t, yaml))
			if err != nil {
				t.Fatal(err)
			}
			if int64(got.Notification.InitialCheckDelaySec) != tt.want {
				t.Errorf("initial delay = %d, want %d", got.Notification.InitialCheckDelaySec, tt.want)
			}
			if got.Notification.ScheduledTimeSec != 37 {
				t.Errorf("repeat interval = %d, want 37", got.Notification.ScheduledTimeSec)
			}
			if delay := time.Duration(got.Notification.InitialCheckDelaySec) * time.Second; delay < 0 || int64(delay/time.Second) != tt.want {
				t.Errorf("duration conversion = %v, want %d seconds", delay, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidInitialCheckDelay(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		want    string
	}{
		{"negative", "-1", "notification.initial_check_delay_sec must be between"},
		{"duration overflow", strconv.FormatInt(math.MaxInt64/int64(time.Second)+1, 10), "notification.initial_check_delay_sec must be between"},
		{"maximum int64", strconv.FormatInt(math.MaxInt64, 10), "notification.initial_check_delay_sec must be between"},
		{"integer overflow", "9223372036854775808", "parse config"},
		{"integer underflow", "-9223372036854775809", "must be an integer number of seconds"},
		{"negative fraction", "-0.5", "must be an integer number of seconds"},
		{"fraction", "1.5", "must be an integer number of seconds"},
		{"whole float", "1.0", "must be an integer number of seconds"},
		{"quoted integer", "\"3600\"", "must be an integer number of seconds"},
		{"duration string", "2h", "must be an integer number of seconds"},
		{"boolean", "true", "must be an integer number of seconds"},
		{"sequence", "[1]", "must be an integer number of seconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(validYAML, "notification:\n", "notification:\n  initial_check_delay_sec: "+tt.setting+"\n", 1)
			_, err := config.Load(writeConfig(t, yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

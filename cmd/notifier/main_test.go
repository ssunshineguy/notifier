package main

import (
	"bytes"
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidInvocation(t *testing.T) {
	configPath := writeCLIConfig(t)
	tests := []struct {
		name      string
		args      []string
		wantError string
		wantUsage bool
	}{
		{"missing mode", nil, "exactly one mode is required", true},
		{"extra mode", []string{"api", "migrate"}, "exactly one mode is required", true},
		{"unknown mode", []string{"-config", configPath, "unknown"}, "unknown mode \"unknown\"", false},
		{"worker", []string{"-config", configPath, "worker"}, "mode \"worker\" is not implemented yet", false},
		{"collector", []string{"-config", configPath, "collector"}, "mode \"collector\" is not implemented yet", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := setCommandLine(t, tt.args...)
			err := run()
			if err == nil || err.Error() != tt.wantError {
				t.Fatalf("run = %v, want %q", err, tt.wantError)
			}
			if tt.wantUsage && !strings.Contains(output.String(), "Usage: notifier") {
				t.Errorf("output = %q, want usage", output.String())
			}
		})
	}
}

func TestRunReportsMissingConfig(t *testing.T) {
	setCommandLine(t, "-config", filepath.Join(t.TempDir(), "missing.yaml"), "api")
	if err := run(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run = %v, want os.ErrNotExist", err)
	}
}

func TestRunAppliesListenAddressOverride(t *testing.T) {
	configPath := writeCLIConfig(t)
	const invalidAddress = "127.0.0.1:8080:extra"
	setCommandLine(t, "-config", configPath, "-listen-addr", invalidAddress, "api")

	err := run()
	var addrErr *net.AddrError
	if !errors.As(err, &addrErr) || addrErr.Addr != invalidAddress {
		t.Fatalf("run = %v, want an error for the overridden address %q", err, invalidAddress)
	}
}

func setCommandLine(t *testing.T, args ...string) *bytes.Buffer {
	t.Helper()
	previousArgs, previousFlags, previousUsage := os.Args, flag.CommandLine, flag.Usage
	t.Cleanup(func() {
		os.Args, flag.CommandLine, flag.Usage = previousArgs, previousFlags, previousUsage
	})
	os.Args = append([]string{"notifier"}, args...)
	flag.CommandLine = flag.NewFlagSet("notifier", flag.ContinueOnError)
	var output bytes.Buffer
	flag.CommandLine.SetOutput(&output)
	return &output
}

func writeCLIConfig(t *testing.T) string {
	t.Helper()
	const yaml = `api:
  listen_addr: 127.0.0.1:8080:fixture
database:
  min_conns: 0
  migrator_dsn: postgres://127.0.0.1/notifier
  api_dsn: postgres://127.0.0.1/notifier
  worker_dsn: postgres://127.0.0.1/notifier
  collector_dsn: postgres://127.0.0.1/notifier
notification:
  scheduled_time_sec: 1
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

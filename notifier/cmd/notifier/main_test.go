package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidInvocation(t *testing.T) {
	configPath := writeCLIConfig(t, cliTestMigratorDSN)
	tests := []struct {
		name      string
		args      []string
		wantError string
		wantUsage bool
	}{
		{"missing mode", nil, "exactly one mode is required", true},
		{"extra mode", []string{"api", "migrate"}, "exactly one mode is required", true},
		{"unknown mode", []string{"-config", configPath, "unknown"}, "unknown mode \"unknown\"", false},
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

func TestRunWithLoggingRedactsStartupError(t *testing.T) {
	t.Setenv("NOTIFIER_LOG_LEVEL", "INFO")
	const marker = "private-application-name"
	configPath := writeCLIConfig(t, cliTestMigratorDSN+"?port=invalid&application_name="+marker)
	setCommandLine(t, "-config", configPath, "migrate")

	logFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	previousStderr := os.Stderr
	os.Stderr = logFile
	t.Cleanup(func() {
		os.Stderr = previousStderr
		if err := logFile.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := runWithLogging(); err == nil || strings.Contains(err.Error(), marker) {
		t.Fatal("expected a safe startup error without the synthetic value")
	}

	data, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		Message string `json:"msg"`
		Level   string `json:"level"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Message != "notifier failed" || event.Level != "ERROR" || event.Error == "" || bytes.Contains(data, []byte(marker)) {
		t.Fatal("expected a structured startup error without the synthetic value")
	}
}

func TestRunReportsMissingConfig(t *testing.T) {
	setCommandLine(t, "-config", filepath.Join(t.TempDir(), "missing.yaml"), "api")
	if err := run(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run = %v, want os.ErrNotExist", err)
	}
}

func TestRunAppliesListenAddressOverride(t *testing.T) {
	configPath := writeCLIConfig(t, cliTestMigratorDSN)
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

const cliTestMigratorDSN = "postgres://127.0.0.1/notifier"

func writeCLIConfig(t *testing.T, migratorDSN string) string {
	t.Helper()
	yaml := `api:
  listen_addr: 127.0.0.1:8080:fixture
database:
  min_conns: 0
notification:
  scheduled_time_sec: 1
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"api", "worker", "collector", "migrate"} {
		dsn := cliTestMigratorDSN
		if mode == "migrate" {
			dsn = migratorDSN
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), mode+".dsn"), []byte(dsn), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestRunUsesExplicitDSNFile(t *testing.T) {
	configPath := writeCLIConfig(t, cliTestMigratorDSN)
	if err := os.Remove(filepath.Join(filepath.Dir(configPath), "api.dsn")); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(filepath.Dir(configPath), "explicit.dsn")
	if err := os.WriteFile(selected, []byte("postgres://collector@127.0.0.1:1/notifier?sslmode=disable"), 0o600); err != nil {
		t.Fatal(err)
	}
	setCommandLine(t, "-config", configPath, "-database-dsn-file", selected, "-listen-addr", "127.0.0.1:8080:explicit", "api")
	var addrErr *net.AddrError
	if err := run(); !errors.As(err, &addrErr) || addrErr.Addr != "127.0.0.1:8080:explicit" {
		t.Fatalf("run = %v, want the explicit listen address error after successful config loading", err)
	}
}

func TestRunWithLoggingExplainsMissingSelectedDSN(t *testing.T) {
	for _, mode := range []string{"api", "worker", "collector", "migrate"} {
		t.Run(mode, func(t *testing.T) {
			configPath := writeCLIConfig(t, cliTestMigratorDSN)
			if err := os.Remove(filepath.Join(filepath.Dir(configPath), mode+".dsn")); err != nil {
				t.Fatal(err)
			}
			setCommandLine(t, "-config", configPath, mode)
			logFile, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			previousStderr := os.Stderr
			os.Stderr = logFile
			t.Cleanup(func() {
				os.Stderr = previousStderr
				if err := logFile.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := runWithLogging(); err == nil {
				t.Fatal("missing selected DSN did not prevent startup")
			}
			data, err := os.ReadFile(logFile.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(mode+" database")) || !bytes.Contains(data, []byte("-database-dsn-file")) {
				t.Fatalf("missing DSN log does not explain how to configure the selected mode: %s", data)
			}
		})
	}
}

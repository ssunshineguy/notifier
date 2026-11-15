//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerProcessExitsAfterSIGTERMWithBlockedPool(t *testing.T) {
	config := "worker:\n  workers: 1\n  cycle_timeout_ms: 6000\ndatabase:\n  min_conns: 2\n  max_conns: 2\nnotification:\n  scheduled_time_sec: 1\n  lock_duration_ms: 6000\n"
	testProcessExitsAfterSIGTERMWithBlockedPool(t, "worker", config)
}

func testProcessExitsAfterSIGTERMWithBlockedPool(t *testing.T, mode, config string) {
	t.Helper()
	configEnv := "NOTIFIER_TEST_" + strings.ToUpper(mode) + "_CONFIG"
	if configPath := os.Getenv(configEnv); configPath != "" {
		setCommandLine(t, "-config", configPath, mode)
		main()
		return
	}

	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	dsn := "postgres://" + mode + "@" + listener.Addr().String() + "/notifier?sslmode=disable&connect_timeout=60"
	if err := os.WriteFile(filepath.Join(dir, mode+".dsn"), []byte(dsn), 0o600); err != nil {
		t.Fatal(err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^"+t.Name()+"$") //nolint:gosec // This re-runs the current test executable.
	cmd.Env = append(cmd.Environ(), configEnv+"="+configPath, "NOTIFIER_LOG_LEVEL=ERROR")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	// Each fixture allows at most one runtime query before the accept deadline.
	// At least one connection is from pool warmup, whose constructor survives
	// cancellation and must be covered by the process exit timer.
	for range 2 {
		conn, err := listener.AcceptTCP()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
	}

	sentAt := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	elapsed := time.Since(sentAt)

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || elapsed < 10*time.Second || elapsed > 11*time.Second {
		t.Fatalf("%s exit = %v after %v, want exit code 1 after 10s with up to 1s scheduling tolerance; output: %s", mode, err, elapsed, output.String())
	}
}

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestLoadOnlySelectedModeDSN(t *testing.T) {
	for _, mode := range []string{"api", "worker", "collector", "migrate"} {
		t.Run(mode, func(t *testing.T) {
			path := writeModeConfig(t)
			dsn := "postgres://" + mode + "@localhost/notifier?sslmode=disable"
			writeDSN(t, filepath.Join(filepath.Dir(path), mode+".dsn"), dsn+"\n")
			got, err := config.Load(path, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Database.DSN != dsn {
				t.Fatal("loaded DSN does not match the selected mode")
			}
		})
	}
}

func TestLoadRejectsMissingEmptyAndInvalidSelectedDSN(t *testing.T) {
	for _, mode := range []string{"api", "worker", "collector", "migrate"} {
		for _, dsn := range []string{"missing", "", " \n\t", "postgres://role:synthetic-secret@localhost:invalid/notifier"} {
			t.Run(mode+"/"+map[bool]string{true: "missing", false: "present"}[dsn == "missing"], func(t *testing.T) {
				path := writeModeConfig(t)
				if dsn != "missing" {
					writeDSN(t, filepath.Join(filepath.Dir(path), mode+".dsn"), dsn)
				}
				_, err := config.Load(path, mode, "")
				var databaseError *config.DatabaseConfigError
				if !errors.As(err, &databaseError) || !strings.Contains(err.Error(), mode+" database") {
					t.Fatalf("Load = %v, want selected-mode database error", err)
				}
				if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), path) {
					t.Fatal("database error contains secret material or a file path")
				}
			})
		}
	}
}

func TestLoadDSNOverrideDoesNotReadDefaultFile(t *testing.T) {
	path := writeModeConfig(t)
	writeDSN(t, filepath.Join(filepath.Dir(path), "api.dsn"), "invalid ignored default")
	selected := filepath.Join(t.TempDir(), "selected.dsn")
	const dsn = "postgres://api@localhost/notifier"
	writeDSN(t, selected, dsn)
	got, err := config.Load(path, "api", selected)
	if err != nil {
		t.Fatal(err)
	}
	if got.Database.DSN != dsn {
		t.Fatal("loader did not use the explicitly selected DSN file")
	}
}

func TestLoadRejectsUnknownModeBeforeReadingFiles(t *testing.T) {
	_, err := config.Load("missing.yaml", "../api", "")
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("Load = %v, want unknown mode", err)
	}
}

func TestLoadRejectsDSNsInSharedConfig(t *testing.T) {
	for _, field := range []string{"dsn", "api_dsn", "worker_dsn", "collector_dsn", "migrator_dsn"} {
		path := writeModeConfig(t)
		yaml := strings.Replace(validYAML, "database:\n", "database:\n  "+field+": postgres://local/notifier\n", 1)
		if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path, "api", ""); err == nil || !strings.Contains(err.Error(), "field "+field+" not found") {
			t.Fatalf("Load = %v, want rejected inline DSN", err)
		}
	}
}

func writeModeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDSN(t *testing.T, path, dsn string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(dsn), 0o600); err != nil {
		t.Fatal(err)
	}
}

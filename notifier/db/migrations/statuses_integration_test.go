//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // Registers the pgx database/sql driver.
	"github.com/pressly/goose/v3"

	"github.com/ssunshineguy/notifier/notifier/db/migrations"
)

func TestSplitStatusesFreshIntegration(t *testing.T) {
	db := newMigrationDatabase(t)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertFinalStatusSchema(t, db)
	assertVersion(t, provider, 2)
	if result, err := provider.Up(t.Context()); err != nil || len(result) != 0 {
		t.Fatalf("second Up = %d migrations, %v; want no work", len(result), err)
	}
}

func assertFinalStatusSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var columns, validated int
	if err := db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'notifier' AND table_name = 't_notification' AND (
			(column_name = 'notification_status' AND udt_name = 'text' AND is_nullable = 'NO'
				AND column_default = '''active''::text') OR
			(column_name = 'delivery_status' AND udt_name = 'text' AND is_nullable = 'YES')
		)`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'notifier.t_notification'::regclass AND convalidated AND conname IN (
			'ck_notification_notification_status', 'ck_notification_delivery_status',
			'ck_notification_notification_status_delivery_status', 'ck_notification_stop_dt_delivery_status'
		)`).Scan(&validated); err != nil {
		t.Fatal(err)
	}
	var legacy bool
	if err := db.QueryRowContext(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns WHERE table_schema = 'notifier'
		AND table_name = 't_notification' AND column_name = 'stop_reason')`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if columns != 2 || validated != 4 || legacy {
		t.Fatalf("final schema: valid status columns=%d, validated checks=%d, legacy column=%t", columns, validated, legacy)
	}
}

func assertVersion(t *testing.T, provider *goose.Provider, want int64) {
	t.Helper()
	got, err := provider.GetDBVersion(t.Context())
	if err != nil || got != want {
		t.Fatalf("database version = %d, %v; want %d", got, err, want)
	}
}

func execMigrationSQL(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}

func newMigrationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	addr := os.Getenv("NOTIFIER_MIGRATION_TEST_ADDR")
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("NOTIFIER_MIGRATION_TEST_ADDR must be the loopback address from docker compose port postgres 5432")
	}
	dsn := url.URL{Scheme: "postgres", Host: addr, Path: "/postgres", User: url.UserPassword("postgres", "postgres"), RawQuery: "sslmode=disable"}
	admin := openMigrationDB(t, dsn.String())
	name := "notifier_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{name}.Sanitize()
	execMigrationSQL(t, admin, "CREATE DATABASE "+identifier+" OWNER notifier_migrator")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+identifier+" WITH (FORCE)"); err != nil {
			t.Errorf("drop scratch database: %v", err)
		}
	})
	dsn.Path = "/" + name
	dsn.User = url.UserPassword("notifier_migrator", "notifier_migrator")
	db := openMigrationDB(t, dsn.String())
	execMigrationSQL(t, db, "CREATE SCHEMA notifier AUTHORIZATION notifier_migrator")
	return db
}

func openMigrationDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(fmt.Errorf("close migration test database: %w", err))
		}
	})
	return db
}

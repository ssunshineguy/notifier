// Package application wires dependencies and runs the service modes.
package application

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	// Registers the "pgx" driver for database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/ssunshineguy/notifier/notifier/db/migrations"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, cfg config.Config) error {
	db, err := sql.Open("pgx", cfg.Database.MigratorDSN)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("close database", "error", err)
		}
	}()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	for _, result := range results {
		slog.Info("migration applied",
			"version", result.Source.Version,
			"file", result.Source.Path,
			"duration", result.Duration,
		)
	}
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	if len(results) == 0 {
		slog.Info("database is up to date")
	}

	return nil
}

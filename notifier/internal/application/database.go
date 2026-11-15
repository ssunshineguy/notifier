package application

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func newDatabasePool(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, errors.New("database DSN is required")
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, errors.New("invalid database connection string")
	}
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConns = cfg.MaxConns
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, errors.New("open database pool failed")
	}
	return pool, nil
}

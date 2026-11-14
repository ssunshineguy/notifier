// Package postgres stores notifications and reads clients in PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/internal/logging"
)

// Store runs the queries of one service mode over its own connection pool.
type Store struct {
	pool             *pgxpool.Pool
	operationTimeout time.Duration
}

// NewStore applies an operation timeout to queries on the caller-owned pool.
func NewStore(pool *pgxpool.Pool, operationTimeout time.Duration) *Store {
	return &Store{pool: pool, operationTimeout: operationTimeout}
}

// CheckConnection verifies that PostgreSQL can execute SQL within the store's operation budget.
func (s *Store) CheckConnection(ctx context.Context) error {
	var result int
	return s.scanRowWithLogLevel(ctx, slog.LevelDebug, "check_connection", "SELECT 1", nil, func() slog.Attr {
		return slog.Int("value", result)
	}, &result)
}

func (s *Store) scanRow(ctx context.Context, queryName, query string, args []any, result func() slog.Attr, dest ...any) error {
	return s.scanRowWithLogLevel(ctx, slog.LevelInfo, queryName, query, args, result, dest...)
}

func (s *Store) scanRowWithLogLevel(ctx context.Context, successLevel slog.Level, queryName, query string, args []any, result func() slog.Attr, dest ...any) error {
	start := time.Now()
	dbCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()

	logger := logging.FromContext(ctx)
	logger.Log(ctx, successLevel, "Start SQL "+queryName, "query_name", queryName, "argument_count", len(args))
	err := s.pool.QueryRow(dbCtx, query, args...).Scan(dest...)
	endLevel := successLevel
	if err != nil {
		endLevel = slog.LevelInfo
	}
	if !logger.Enabled(ctx, endLevel) {
		return err
	}

	durationMS := time.Since(start).Milliseconds()
	endMessage := fmt.Sprintf("End SQL %s duration %d ms", queryName, durationMS)
	logger = logger.With("query_name", queryName, "duration_ms", durationMS)
	switch {
	case err == nil:
		logger.LogAttrs(ctx, endLevel, endMessage,
			slog.String("outcome", "ok"), slog.Int("row_count", 1),
			slog.Group("result", result()))
	case errors.Is(err, pgx.ErrNoRows):
		logger.InfoContext(ctx, endMessage, "outcome", "no_rows", "row_count", 0)
	default:
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			logger.InfoContext(ctx, endMessage, "outcome", "error", "sqlstate", postgresError.Code)
		} else {
			logger.InfoContext(ctx, endMessage, "outcome", "error", "error_type", fmt.Sprintf("%T", err))
		}
	}
	return err
}

// Package postgres stores notifications and reads clients in PostgreSQL.
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

func (s *Store) scanRow(ctx context.Context, query string, args []any, dest ...any) error {
	dbCtx, cancel := context.WithTimeout(ctx, s.operationTimeout)
	defer cancel()

	return s.pool.QueryRow(dbCtx, query, args...).Scan(dest...)
}

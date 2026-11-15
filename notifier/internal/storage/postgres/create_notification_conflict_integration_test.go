//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestCreateNotificationConflictLookupIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 2*time.Second)
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	for _, scenario := range []string{"deleted duplicate", "canceled lookup", "lookup operation timeout", "lookup PostgreSQL error"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			notification := core.Notification{
				ClientID: "owner", ExternalID: scenario,
				Start: start, Deadline: start.Add(time.Hour), NextScheduled: start,
				Params: []core.Condition{{Type: core.ConditionTime, Time: core.TimeCondition{
					Operation: core.GreaterThan, Threshold: start.Add(time.Minute),
				}}},
			}
			existingID, err := store.CreateNotification(ctx, notification)
			if err != nil {
				t.Fatal(err)
			}

			requestCtx, cancelRequest := context.WithCancel(ctx)
			defer cancelRequest()
			var lockTx pgx.Tx
			defer func() {
				if lockTx != nil {
					if err := lockTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
						t.Errorf("release lookup lock: %v", err)
					}
				}
			}()
			trace := &duplicateLookupTrace{beforeLookup: func() error {
				switch scenario {
				case "deleted duplicate":
					tag, err := pool.Exec(ctx, "DELETE FROM notifier.t_notification WHERE id = $1", existingID)
					if err != nil {
						return err
					}
					if tag.RowsAffected() != 1 {
						return fmt.Errorf("deleted %d rows, want the existing duplicate", tag.RowsAffected())
					}
				case "canceled lookup":
					cancelRequest()
				default:
					lockTx, err = pool.Begin(ctx)
					if err != nil {
						return err
					}
					_, err = lockTx.Exec(ctx, "LOCK TABLE notifier.t_notification IN ACCESS EXCLUSIVE MODE")
					return err
				}
				return nil
			}}
			poolConfig := pool.Config()
			poolConfig.ConnConfig.Tracer = trace
			operationTimeout := 2 * time.Second
			switch scenario {
			case "lookup operation timeout":
				operationTimeout = 100 * time.Millisecond
			case "lookup PostgreSQL error":
				poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = "100"
			}
			tracedPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(tracedPool.Close)
			if err := tracedPool.Ping(ctx); err != nil {
				t.Fatal(err)
			}
			conflictingStore := postgres.NewStore(tracedPool, operationTimeout)
			id, createErr := conflictingStore.CreateNotification(requestCtx, notification)
			if lockTx != nil {
				if err := lockTx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				lockTx = nil
			}
			if trace.hookErr != nil {
				t.Fatalf("coordinate conflict lookup: %v", trace.hookErr)
			}
			if trace.inserts != 1 || trace.lookups != 1 || !trace.conflictConfirmed {
				t.Fatalf("query sequence: inserts=%d lookups=%d confirmed conflict=%t, want one of each", trace.inserts, trace.lookups, trace.conflictConfirmed)
			}
			if id != uuid.Nil {
				t.Fatalf("CreateNotification ID = %s, want no created notification", id)
			}
			switch scenario {
			case "deleted duplicate":
				if !errors.Is(createErr, core.ErrNotificationDuplicateDisappeared) {
					t.Fatalf("CreateNotification = %v, want disappeared duplicate", createErr)
				}
			case "canceled lookup":
				if !errors.Is(createErr, context.Canceled) {
					t.Fatalf("CreateNotification = %v, want cancellation", createErr)
				}
			case "lookup operation timeout":
				if !errors.Is(createErr, context.DeadlineExceeded) {
					t.Fatalf("CreateNotification = %v, want operation timeout", createErr)
				}
			case "lookup PostgreSQL error":
				var dbErr *pgconn.PgError
				if !errors.As(createErr, &dbErr) || dbErr.Code != "57014" {
					t.Fatalf("CreateNotification = %v, want PostgreSQL statement timeout", createErr)
				}
			}
			if scenario != "deleted duplicate" && errors.Is(createErr, core.ErrNotificationDuplicateDisappeared) {
				t.Fatalf("lookup failure misclassified as disappeared duplicate: %v", createErr)
			}
			storedID, found, err := store.FindNotificationID(ctx, notification.ClientID, notification.ExternalID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "deleted duplicate" {
				if found || storedID != uuid.Nil {
					t.Fatalf("lookup after conflict = (%s, %t), want no replacement notification", storedID, found)
				}
			} else if !found || storedID != existingID {
				t.Fatalf("lookup after failure = (%s, %t), want original notification %s", storedID, found, existingID)
			}
		})
	}
}

type duplicateQueryKey struct{}

type duplicateLookupTrace struct {
	beforeLookup      func() error
	inserts           int
	lookups           int
	conflictConfirmed bool
	hookErr           error
}

func (tr *duplicateLookupTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	query := strings.TrimSpace(data.SQL)
	if strings.HasPrefix(query, "INSERT INTO notifier.t_notification") {
		tr.inserts++
	}
	if strings.HasPrefix(query, "SELECT id FROM notifier.t_notification") {
		tr.lookups++
		if !tr.conflictConfirmed {
			tr.hookErr = errors.New("lookup started before INSERT confirmed a conflict")
		} else {
			tr.hookErr = tr.beforeLookup()
		}
		if tr.hookErr != nil {
			canceledCtx, cancel := context.WithCancel(ctx)
			cancel()
			return canceledCtx
		}
	}
	return context.WithValue(ctx, duplicateQueryKey{}, query)
}

func (tr *duplicateLookupTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	query, _ := ctx.Value(duplicateQueryKey{}).(string)
	if strings.HasPrefix(query, "INSERT INTO notifier.t_notification") {
		tr.conflictConfirmed = data.Err == nil && data.CommandTag.Insert() && data.CommandTag.RowsAffected() == 0
	}
}

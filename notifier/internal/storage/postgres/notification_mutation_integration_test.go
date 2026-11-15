//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestNotificationMutationsRespectWorkerReservationIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 5*time.Second)
	const owner = "reservation-owner"
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	original := []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 5}}}
	replacement := []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}}

	for _, op := range []struct {
		name   string
		mutate func(context.Context, uuid.UUID, string) error
	}{
		{name: "PUT", mutate: func(ctx context.Context, id uuid.UUID, clientID string) error {
			_, err := store.UpdateNotification(ctx, id, clientID, replacement)
			return err
		}},
		{name: "DELETE", mutate: store.DeleteNotification},
	} {
		t.Run(op.name, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				lease time.Duration
				wait  bool
			}{
				{name: "worker claim commits during request", lease: time.Minute},
				{name: "reservation expires while API waits", lease: 2 * time.Second, wait: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
					defer cancel()
					id, err := store.CreateNotification(ctx, core.Notification{
						ClientID: owner, ExternalID: op.name + " " + tc.name, Params: original,
						Start: start, Deadline: start.Add(time.Hour), NextScheduled: start,
					})
					if err != nil {
						t.Fatal(err)
					}
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := tx.Rollback(t.Context()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
							t.Error(err)
						}
					}()
					var leaseUntil time.Time
					if err := tx.QueryRow(ctx, `UPDATE notifier.t_notification
						SET lock_until_dt = clock_timestamp() + $2::bigint * interval '1 millisecond'
						WHERE id = $1 RETURNING lock_until_dt`, id, tc.lease.Milliseconds()).Scan(&leaseUntil); err != nil {
						t.Fatal(err)
					}

					done := make(chan error, 1)
					go func() { done <- op.mutate(ctx, id, owner) }()
					waitForBlockedNotificationMutation(t, ctx, pool, tx, done)
					if tc.wait {
						var unexpired bool
						if err := pool.QueryRow(ctx, "SELECT clock_timestamp() < $1", leaseUntil).Scan(&unexpired); err != nil || !unexpired {
							t.Fatalf("reservation active while API waits = %t, %v", unexpired, err)
						}
						if _, err := tx.Exec(ctx, "SELECT pg_sleep_until($1)", leaseUntil); err != nil {
							t.Fatal(err)
						}
					}
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}

					err = <-done
					if !tc.wait {
						if !errors.Is(err, core.ErrNotificationReserved) {
							t.Fatalf("mutation error = %v, want ErrNotificationReserved", err)
						}
						if err := op.mutate(ctx, id, "another-client"); !errors.Is(err, core.ErrNotificationNotFound) {
							t.Errorf("foreign mutation error = %v, want ErrNotificationNotFound", err)
						}
						got, err := store.GetNotification(ctx, id, owner)
						if err != nil || !reflect.DeepEqual(got.Params, original) {
							t.Errorf("after conflict got notification %+v, %v; want unchanged record", got, err)
						}
						return
					}
					if err != nil {
						t.Fatalf("mutation after reservation expires = %v, want success", err)
					}
					got, err := store.GetNotification(ctx, id, owner)
					if op.name == "DELETE" {
						if !errors.Is(err, core.ErrNotificationNotFound) {
							t.Errorf("GET after DELETE = %v, want ErrNotificationNotFound", err)
						}
					} else if err != nil || !reflect.DeepEqual(got.Params, replacement) {
						t.Errorf("after PUT got notification %+v, %v; want new conditions", got, err)
					}
				})
			}
		})
	}
}

func waitForBlockedNotificationMutation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tx pgx.Tx, done <-chan error) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))
		)`, tx.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("mutation finished while the row was locked: %v", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

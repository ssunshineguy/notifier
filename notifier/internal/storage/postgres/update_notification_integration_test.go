//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestUpdateNotificationIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 5*time.Second)
	ctx := t.Context()
	const owner = "update-owner"
	const externalID = "update-external-id"
	start := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	deadline := start.Add(24 * time.Hour)
	scheduled := start.Add(time.Minute)
	var lockUntil time.Time
	if err := pool.QueryRow(ctx, "SELECT clock_timestamp() - interval '1 minute'").Scan(&lockUntil); err != nil {
		t.Fatal(err)
	}
	original := []core.Condition{{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: start.Add(time.Hour)}}}
	replacement := []core.Condition{{Type: core.ConditionAnd, Conditions: []core.Condition{
		{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: start.Add(2 * time.Hour)}},
		{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.LessThan, Threshold: start.Add(3 * time.Hour)}},
		{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: 19990}},
	}}}
	requestedReplacement := []core.Condition{{Type: core.ConditionAnd, Conditions: append([]core.Condition(nil), replacement[0].Conditions...)}}
	requestedReplacement[0].Conditions[0].Time.Threshold = start.Add(2*time.Hour + 900*time.Millisecond)
	id, err := store.CreateNotification(ctx, core.Notification{
		ClientID: owner, ExternalID: externalID, Params: original,
		Start: start, Deadline: deadline, NextScheduled: scheduled,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE notifier.t_notification SET lock_until_dt = $1, failed_attempt_count = 2 WHERE id = $2`, lockUntil, id)
	if err != nil {
		t.Fatal(err)
	}

	assertNotification := func(t *testing.T, notificationStatus, deliveryStatus string, got core.NotificationDetails) {
		t.Helper()
		if got.ID != id || got.ExternalID != externalID || got.NotificationStatus != notificationStatus || got.DeliveryStatus != deliveryStatus ||
			!got.Start.Equal(start) || !got.Deadline.Equal(deadline) || !reflect.DeepEqual(got.Params, replacement) {
			t.Fatalf("unexpected notification: %+v", got)
		}
	}
	assertStored := func(t *testing.T, notificationStatus, deliveryStatus string) {
		t.Helper()
		got, err := store.GetNotification(t.Context(), id, owner)
		if err != nil {
			t.Fatal(err)
		}
		assertNotification(t, notificationStatus, deliveryStatus, got)

		var gotScheduled, gotLock time.Time
		var attempts int
		err = pool.QueryRow(t.Context(), `
			SELECT next_scheduled_dt, lock_until_dt, failed_attempt_count
			FROM notifier.t_notification WHERE id = $1`, id).Scan(&gotScheduled, &gotLock, &attempts)
		if err != nil {
			t.Fatal(err)
		}
		if !gotScheduled.Equal(scheduled) || !gotLock.Equal(lockUntil) || attempts != 2 {
			t.Errorf("worker state changed: %v, %v, %d", gotScheduled, gotLock, attempts)
		}
	}

	t.Run("replace and repeat after reservation expires", func(t *testing.T) {
		for range 2 {
			got, err := store.UpdateNotification(t.Context(), id, owner, requestedReplacement)
			if err != nil {
				t.Fatal(err)
			}
			assertNotification(t, core.NotificationActive, "", got)
		}
		assertStored(t, core.NotificationActive, "")
	})
	t.Run("foreign and missing", func(t *testing.T) {
		if _, err := store.UpdateNotification(t.Context(), id, "another-client", original); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Fatalf("foreign update = %v, want ErrNotificationNotFound", err)
		}
		if _, err := store.UpdateNotification(t.Context(), uuid.New(), owner, original); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Fatalf("missing update = %v, want ErrNotificationNotFound", err)
		}
		assertStored(t, core.NotificationActive, "")
	})
	t.Run("stop racing with update", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
		defer cancel()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tx.Rollback(t.Context()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error(err)
			}
		}()
		_, err = tx.Exec(ctx, `UPDATE notifier.t_notification
			SET stop_dt = now(), notification_status = 'success', delivery_status = 'delivered' WHERE id = $1`, id)
		if err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() {
			_, err := store.UpdateNotification(ctx, id, owner, original)
			done <- err
		}()

		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			err := pool.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))
			)`, tx.Conn().PgConn().PID()).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("update completed before the stop committed: %v", err)
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}

		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, core.ErrNotificationStopped) {
			t.Fatalf("racing update = %v, want ErrNotificationStopped", err)
		}
		assertStored(t, core.EventSuccess, core.DeliveryDelivered)
	})
	for _, state := range stoppedNotificationStates {
		t.Run(state.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification
				SET stop_dt = now(), notification_status = $1, delivery_status = NULLIF($2, '') WHERE id = $3`,
				state.notificationStatus, state.deliveryStatus, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpdateNotification(t.Context(), id, owner, original); !errors.Is(err, core.ErrNotificationStopped) {
				t.Fatalf("stopped update = %v, want ErrNotificationStopped", err)
			}
			if _, err := store.UpdateNotification(t.Context(), id, "another-client", original); !errors.Is(err, core.ErrNotificationNotFound) {
				t.Fatalf("foreign stopped update = %v, want ErrNotificationNotFound", err)
			}
			assertStored(t, state.notificationStatus, state.deliveryStatus)
		})
	}
	t.Run("database error", func(t *testing.T) {
		pool.Close()
		_, err := store.UpdateNotification(t.Context(), id, owner, original)
		if err == nil || !strings.HasPrefix(err.Error(), "update notification:") {
			t.Fatalf("UpdateNotification = %v, want storage error", err)
		}
	})
}

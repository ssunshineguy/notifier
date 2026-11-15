//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestSaveNotificationResultIntegration(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	store := postgres.NewStore(openTestPool(ctx, t, postgresDSN(address, "notifier_worker")), time.Second)
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	initialSchedule := start.Add(time.Second)
	next := start.Add(10 * time.Minute)
	const clientID = "worker-delivery"
	const callbackURL = "https://example.test/callback"
	if _, err := pool.Exec(ctx, `INSERT INTO notifier.t_client (client_id, callback_url, max_lifetime_sec, callback_timeout_ms)
		VALUES ($1, $2, 3600, 250)`, clientID, callbackURL); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		result  core.NotificationResult
		expired bool
		stale   bool
		stopped bool
		deleted bool
	}{
		{name: "reschedule", result: core.NotificationResult{NextScheduled: &next, NotificationStatus: core.NotificationActive}},
		{name: "retry", result: core.NotificationResult{NextScheduled: &next, FailedAttemptCount: 3,
			NotificationStatus: core.EventSuccess, DeliveryStatus: core.DeliveryTryingToDeliver}},
		{name: "delivered", result: core.NotificationResult{NotificationStatus: core.EventSuccess, DeliveryStatus: core.DeliveryDelivered, FailedAttemptCount: 1}},
		{name: "deadline event delivered", result: core.NotificationResult{NotificationStatus: core.EventDeadlineExceeded, DeliveryStatus: core.DeliveryDelivered}},
		{name: "attempts exhausted", result: core.NotificationResult{NotificationStatus: core.EventSuccess, DeliveryStatus: core.DeliveryFailed, FailedAttemptCount: 42}},
		{name: "expired claim", expired: true},
		{name: "reclaimed by another worker", stale: true},
		{name: "already stopped", stopped: true},
		{name: "deleted record", deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := store.CreateNotification(ctx, core.Notification{
				ClientID: clientID, ExternalID: tc.name, Start: start, Deadline: start.Add(time.Hour), NextScheduled: initialSchedule,
			})
			if err != nil {
				t.Fatal(err)
			}
			lock := start.Add(time.Hour)
			if tc.expired {
				lock = start
			}
			if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET lock_until_dt = $1 WHERE id = $2", lock, id); err != nil {
				t.Fatal(err)
			}
			oldNotificationStatus, oldDeliveryStatus := core.NotificationActive, ""
			if tc.stopped {
				oldNotificationStatus, oldDeliveryStatus = core.EventSuccess, core.DeliveryDelivered
				if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET stop_dt = $1, notification_status = $2, delivery_status = $3 WHERE id = $4",
					start, oldNotificationStatus, oldDeliveryStatus, id); err != nil {
					t.Fatal(err)
				}
			}
			if tc.deleted {
				if _, err := pool.Exec(ctx, "DELETE FROM notifier.t_notification WHERE id = $1", id); err != nil {
					t.Fatal(err)
				}
			}
			claim := core.NotificationClaim{ID: id, LockUntil: lock}
			if tc.stale {
				claim.LockUntil = lock.Add(-time.Second)
			}

			unavailable := tc.expired || tc.stale || tc.stopped || tc.deleted
			target, err := store.GetClaimedCallbackTarget(ctx, claim)
			if unavailable {
				if !errors.Is(err, core.ErrNotificationNotFound) {
					t.Fatalf("GetClaimedCallbackTarget error = %v, want unavailable claim", err)
				}
			} else if err != nil || target.URL != callbackURL || target.Timeout == nil || *target.Timeout != 250*time.Millisecond {
				t.Fatalf("GetClaimedCallbackTarget = %+v, %v, want client URL and 250ms timeout", target, err)
			}
			err = store.SaveNotificationResult(ctx, claim, tc.result)
			if unavailable && !errors.Is(err, core.ErrNotificationNotFound) || !unavailable && err != nil {
				t.Fatalf("SaveNotificationResult error = %v, unavailable = %t", err, unavailable)
			}
			if tc.deleted {
				return
			}

			var gotLock, gotNext, gotStop *time.Time
			var gotNotificationStatus, gotDeliveryStatus string
			var gotFailures int
			err = pool.QueryRow(ctx, `SELECT lock_until_dt, next_scheduled_dt, stop_dt,
				notification_status, COALESCE(delivery_status, ''), failed_attempt_count
				FROM notifier.t_notification WHERE id = $1`, id).
				Scan(&gotLock, &gotNext, &gotStop, &gotNotificationStatus, &gotDeliveryStatus, &gotFailures)
			if err != nil {
				t.Fatal(err)
			}
			if unavailable {
				if !sameTimestamp(gotLock, &lock) || !sameTimestamp(gotNext, &initialSchedule) ||
					(gotStop != nil) != tc.stopped || gotNotificationStatus != oldNotificationStatus || gotDeliveryStatus != oldDeliveryStatus || gotFailures != 0 {
					t.Errorf("lost claim modified row: lock %v, next %v, stop %v, statuses %q/%q, attempts %d",
						gotLock, gotNext, gotStop, gotNotificationStatus, gotDeliveryStatus, gotFailures)
				}
				return
			}
			stopped := tc.result.DeliveryStatus == core.DeliveryDelivered || tc.result.DeliveryStatus == core.DeliveryFailed
			if gotLock != nil || !sameTimestamp(gotNext, tc.result.NextScheduled) || (gotStop != nil) != stopped ||
				gotNotificationStatus != tc.result.NotificationStatus || gotDeliveryStatus != tc.result.DeliveryStatus || gotFailures != tc.result.FailedAttemptCount {
				t.Errorf("saved row: lock %v, next %v, stop %v, statuses %q/%q, attempts %d, want %+v",
					gotLock, gotNext, gotStop, gotNotificationStatus, gotDeliveryStatus, gotFailures, tc.result)
			}
		})
	}

	t.Run("condition edits preserve the status saved for retry", func(t *testing.T) {
		original := []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}}
		revised := []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: 8}}}
		id, err := store.CreateNotification(ctx, core.Notification{
			ClientID: clientID, ExternalID: "status-persistence", Start: start, Deadline: start.Add(time.Second),
			NextScheduled: initialSchedule, Params: original,
		})
		if err != nil {
			t.Fatal(err)
		}
		claim := core.NotificationClaim{ID: id, LockUntil: start.Add(time.Hour)}
		reserve := func() {
			t.Helper()
			if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET lock_until_dt = $1 WHERE id = $2", claim.LockUntil, id); err != nil {
				t.Fatal(err)
			}
		}
		reserve()
		result := core.NotificationResult{
			NextScheduled: &next, FailedAttemptCount: 1, NotificationStatus: core.EventSuccess, DeliveryStatus: core.DeliveryTryingToDeliver,
		}
		if err := store.SaveNotificationResult(ctx, claim, result); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetNotification(ctx, id, clientID)
		if err != nil || got.NotificationStatus != core.EventSuccess || got.DeliveryStatus != core.DeliveryTryingToDeliver ||
			!reflect.DeepEqual(got.Params, original) {
			t.Fatalf("GetNotification after failed delivery = %+v, %v", got, err)
		}
		updated, err := store.UpdateNotification(ctx, id, clientID, revised)
		if err != nil || updated.NotificationStatus != core.EventSuccess || updated.DeliveryStatus != core.DeliveryTryingToDeliver ||
			!reflect.DeepEqual(updated.Params, revised) {
			t.Fatalf("UpdateNotification during retry = %+v, %v", updated, err)
		}
		claim.LockUntil = claim.LockUntil.Add(time.Second)
		reserve()
		stored, err := store.GetClaimedNotificationConditions(ctx, claim)
		if err != nil || stored.NotificationStatus != core.EventSuccess || stored.FailedAttemptCount != 1 || len(stored.Params) != 0 {
			t.Fatalf("GetClaimedNotificationConditions on retry = %+v, %v", stored, err)
		}
	})

	t.Run("claim with no registered callback client", func(t *testing.T) {
		id, err := store.CreateNotification(ctx, core.Notification{
			ClientID: "missing", ExternalID: "missing-callback", Start: start, Deadline: start.Add(time.Hour), NextScheduled: initialSchedule,
		})
		if err != nil {
			t.Fatal(err)
		}
		lock := start.Add(time.Hour)
		if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET lock_until_dt = $1 WHERE id = $2", lock, id); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetClaimedCallbackTarget(ctx, core.NotificationClaim{ID: id, LockUntil: lock}); !errors.Is(err, core.ErrClientNotFound) {
			t.Errorf("GetClaimedCallbackTarget = %v, want ErrClientNotFound", err)
		}
	})
}

func TestDeliveryRejectsLeaseThatExpiresWhileWaitingForRowIntegration(t *testing.T) {
	pool := startPostgres(t)
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	store := postgres.NewStore(openTestPool(t.Context(), t, postgresDSN(address, "notifier_worker")), 5*time.Second)
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)

	for _, tc := range []struct {
		name string
		run  func(context.Context, core.NotificationClaim) error
	}{
		{name: "callback target", run: func(ctx context.Context, claim core.NotificationClaim) error {
			_, err := store.GetClaimedCallbackTarget(ctx, claim)
			return err
		}},
		{name: "save result", run: func(ctx context.Context, claim core.NotificationClaim) error {
			return store.SaveNotificationResult(ctx, claim, core.NotificationResult{
				NotificationStatus: core.EventSuccess, DeliveryStatus: core.DeliveryDelivered,
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
			defer cancel()
			id, err := store.CreateNotification(ctx, core.Notification{
				ClientID: "worker-delivery", ExternalID: tc.name, Start: start, Deadline: start.Add(time.Hour), NextScheduled: start,
			})
			if err != nil {
				t.Fatal(err)
			}
			claim := core.NotificationClaim{ID: id}
			if err := pool.QueryRow(ctx, `UPDATE notifier.t_notification SET lock_until_dt = clock_timestamp() + interval '2 seconds'
				WHERE id = $1 RETURNING lock_until_dt`, id).Scan(&claim.LockUntil); err != nil {
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
			if _, err := tx.Exec(ctx, "SELECT id FROM notifier.t_notification WHERE id = $1 FOR UPDATE", id); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- tc.run(ctx, claim) }()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting, unexpired bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))),
					clock_timestamp() < $2`, tx.Conn().PgConn().PID(), claim.LockUntil).Scan(&waiting, &unexpired); err != nil {
					t.Fatal(err)
				}
				if !unexpired {
					t.Fatal("operation did not reach the lock before the lease expired")
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("operation completed while the row was locked: %v", err)
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if _, err := tx.Exec(ctx, "SELECT pg_sleep_until($1)", claim.LockUntil); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, core.ErrNotificationNotFound) {
				t.Fatalf("operation returned %v, want ErrNotificationNotFound", err)
			}
			var lock, next, stopped *time.Time
			if err := pool.QueryRow(ctx, "SELECT lock_until_dt, next_scheduled_dt, stop_dt FROM notifier.t_notification WHERE id = $1", id).
				Scan(&lock, &next, &stopped); err != nil {
				t.Fatal(err)
			}
			if !sameTimestamp(lock, &claim.LockUntil) || !sameTimestamp(next, &start) || stopped != nil {
				t.Errorf("expired lease changed row: lock %v, next %v, stop %v", lock, next, stopped)
			}
		})
	}
}

func sameTimestamp(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}

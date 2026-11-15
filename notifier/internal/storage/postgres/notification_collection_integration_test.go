//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestDeleteStoppedNotificationsIntegration(t *testing.T) {
	pool, store := collectionTestStore(t)
	ctx := t.Context()
	cutoff := time.Date(2025, time.November, 15, 12, 30, 0, 0, time.UTC)
	old := cutoff.Add(-time.Microsecond)
	recent := cutoff.Add(time.Microsecond)
	var removed []uuid.UUID
	for i := range 7 {
		status, delivery := core.EventSuccess, core.DeliveryDelivered
		if i%2 == 1 {
			status = core.EventDeadlineExceeded
		}
		if i%3 == 0 {
			delivery = core.DeliveryFailed
		}
		removed = append(removed, seedCollectionNotification(t, pool, "expired-"+strconv.Itoa(i), status, delivery, &old))
	}
	preserved := []uuid.UUID{
		seedCollectionNotification(t, pool, "exact-boundary", core.EventSuccess, core.DeliveryDelivered, &cutoff),
		seedCollectionNotification(t, pool, "recent-stop", core.EventDeadlineExceeded, core.DeliveryFailed, &recent),
		seedCollectionNotification(t, pool, "active", core.NotificationActive, "", nil),
		seedCollectionNotification(t, pool, "legacy-active-stopped", core.NotificationActive, "", &old),
		seedCollectionNotification(t, pool, "pending-success", core.EventSuccess, core.DeliveryTryingToDeliver, nil),
		seedCollectionNotification(t, pool, "pending-deadline", core.EventDeadlineExceeded, core.DeliveryTryingToDeliver, nil),
	}
	for _, limit := range []int{0, -1} {
		count, err := store.DeleteStoppedNotifications(ctx, cutoff, limit)
		if err == nil || count != 0 {
			t.Fatalf("invalid batch limit %d = (%d, %v), want zero deleted and an error", limit, count, err)
		}
	}
	for _, want := range []int64{3, 3, 1, 0, 0} {
		got, err := store.DeleteStoppedNotifications(ctx, cutoff, 3)
		if err != nil || got != want {
			t.Fatalf("DeleteStoppedNotifications = (%d, %v), want (%d, nil)", got, err, want)
		}
	}
	for _, id := range removed {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM notifier.t_notification WHERE id = $1)", id).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("expired notification %s remains", id)
		}
	}
	for _, id := range preserved {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM notifier.t_notification WHERE id = $1)", id).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("protected notification %s was removed", id)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM notifier.t_notification").Scan(&count); err != nil || count != len(preserved) {
		t.Fatalf("remaining notifications = %d, %v, want %d", count, err, len(preserved))
	}
	id, err := store.CreateNotification(ctx, core.Notification{
		ClientID: "collector-fixture", ExternalID: "expired-0", Start: cutoff, Deadline: cutoff.Add(time.Hour), NextScheduled: cutoff,
	})
	if err != nil || id == uuid.Nil || id == removed[0] {
		t.Fatalf("reuse collected external ID = (%s, %v), want a fresh notification", id, err)
	}
	got, found, err := store.FindNotificationID(ctx, "collector-fixture", "expired-0")
	if err != nil || !found || got != id {
		t.Fatalf("reused external ID lookup = (%s, %t, %v), want %s", got, found, err, id)
	}
}

func TestDeleteStoppedNotificationsPreservesDLQAndSnapshotIntegration(t *testing.T) {
	pool, store := collectionTestStore(t)
	ctx := t.Context()
	workerStore := newDLQStore(t, pool, 5*time.Second)
	const owner = "collector-dlq-owner"
	const external = "retained-dlq"
	id := createDLQFixture(t, workerStore, owner, external, false)
	if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET data = '{}' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	claim := reserveDLQFixture(t, pool, id, time.Minute)
	if err := workerStore.MoveNotificationToDLQ(ctx, claim); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC().Add(365 * 24 * time.Hour)
	readPreserved := func() [2]string {
		t.Helper()
		var rows [2]string
		var olderThanCutoff bool
		if err := pool.QueryRow(ctx, `SELECT to_jsonb(notification)::text, to_jsonb(dlq)::text,
            notification.stop_dt < $2 AND dlq.created_dt < $2
            FROM notifier.t_notification AS notification
            JOIN notifier.t_dlq AS dlq ON dlq.notification_id = notification.id
            WHERE notification.id = $1`, id, cutoff).Scan(&rows[0], &rows[1], &olderThanCutoff); err != nil {
			t.Fatal(err)
		}
		if !olderThanCutoff {
			t.Fatal("DLQ fixture must predate the retention cutoff")
		}
		return rows
	}
	before := readPreserved()
	stopped := cutoff.Add(-time.Second)
	for i := range 3 {
		seedCollectionNotification(t, pool, "ordinary-neighbor-"+strconv.Itoa(i), core.EventSuccess, core.DeliveryDelivered, &stopped)
	}
	for _, want := range []int64{2, 1, 0, 0} {
		count, err := store.DeleteStoppedNotifications(ctx, cutoff, 2)
		if err != nil || count != want {
			t.Fatalf("collection beside DLQ = (%d, %v), want (%d, nil)", count, err, want)
		}
		if readPreserved() != before {
			t.Fatal("collection changed the DLQ main row or its complete snapshot")
		}
	}
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM notifier.t_notification").Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("remaining rows = %d, %v, want only the retained DLQ", remaining, err)
	}
	_, err := store.CreateNotification(ctx, core.Notification{
		ClientID: owner, ExternalID: external, Start: cutoff, Deadline: cutoff.Add(time.Hour), NextScheduled: cutoff,
	})
	var duplicate *core.AlreadyExistsError
	if !errors.As(err, &duplicate) || duplicate.ID != id {
		t.Fatalf("reuse of retained DLQ external ID = %v, want original notification conflict", err)
	}
}

func TestDeleteStoppedNotificationsAcceptsBatchAboveDefaultIntegration(t *testing.T) {
	pool, store := collectionTestStore(t)
	ctx := t.Context()
	cutoff := time.Date(2025, time.November, 15, 12, 30, 0, 0, time.UTC)
	start := cutoff.Add(-48 * time.Hour)
	const batchSize = 1001
	_, err := pool.Exec(ctx, `INSERT INTO notifier.t_notification
        (id, client_id, external_id, data, start_dt, deadline_dt, stop_dt, notification_status, delivery_status)
        SELECT gen_random_uuid(), 'collector-fixture', 'large-batch-' || ordinal, '[]', $2, $3, $4, 'success', 'delivered'
        FROM generate_series(1, $1::integer) AS ordinal`,
		batchSize+1, start, start.Add(time.Hour), cutoff.Add(-time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{batchSize, 1, 0} {
		got, err := store.DeleteStoppedNotifications(ctx, cutoff, batchSize)
		if err != nil || got != want {
			t.Fatalf("DeleteStoppedNotifications with limit %d = (%d, %v), want (%d, nil)", batchSize, got, err, want)
		}
	}
}

func TestDeleteStoppedNotificationsSkipsLockedRowsIntegration(t *testing.T) {
	pool, store := collectionTestStore(t)
	ctx := t.Context()
	cutoff := time.Date(2025, time.November, 15, 12, 30, 0, 0, time.UTC)
	stopped := cutoff.Add(-time.Second)
	locked := seedCollectionNotification(t, pool, "locked", core.EventSuccess, core.DeliveryDelivered, &stopped)
	unlocked := seedCollectionNotification(t, pool, "unlocked", core.EventSuccess, core.DeliveryFailed, &stopped)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if _, err := tx.Exec(ctx, "SELECT id FROM notifier.t_notification WHERE id = $1 FOR UPDATE", locked); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{1, 0} {
		got, err := store.DeleteStoppedNotifications(ctx, cutoff, 10)
		if err != nil || got != want {
			t.Fatalf("collection with a row lock = (%d, %v), want (%d, nil)", got, err, want)
		}
	}
	var remaining uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM notifier.t_notification").Scan(&remaining); err != nil || remaining != locked {
		t.Fatalf("remaining = %s, %v, want locked %s; unlocked %s must be deleted", remaining, err, locked, unlocked)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.DeleteStoppedNotifications(ctx, cutoff, 10)
	if err != nil || got != 1 {
		t.Fatalf("retry after lock release = (%d, %v), want (1, nil)", got, err)
	}
}

func TestDeleteStoppedNotificationsCancellationIntegration(t *testing.T) {
	pool, store := collectionTestStore(t)
	ctx := t.Context()
	cutoff := time.Date(2025, time.November, 15, 12, 30, 0, 0, time.UTC)
	stopped := cutoff.Add(-time.Second)
	id := seedCollectionNotification(t, pool, "cancelled-batch", core.EventSuccess, core.DeliveryDelivered, &stopped)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if _, err := tx.Exec(ctx, "LOCK TABLE notifier.t_notification IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		count int64
		err   error
	}
	done := make(chan result, 1)
	go func() {
		count, err := store.DeleteStoppedNotifications(runCtx, cutoff, 10)
		done <- result{count, err}
	}()
	waitCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(waitCtx, `SELECT EXISTS (
            SELECT 1 FROM pg_locks AS lock
            JOIN pg_stat_activity AS activity ON activity.pid = lock.pid
            WHERE activity.usename = 'notifier_collector' AND NOT lock.granted
              AND lock.relation = 'notifier.t_notification'::regclass
        )`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("collector did not wait for the table lock")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case got := <-done:
		if got.count != 0 || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled collection = (%d, %v), want (0, context.Canceled)", got.count, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("collector did not stop after cancellation")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM notifier.t_notification WHERE id = $1)", id).Scan(&exists); err != nil || !exists {
		t.Fatalf("canceled batch record exists = %t, %v, want true", exists, err)
	}
	got, err := store.DeleteStoppedNotifications(ctx, cutoff, 10)
	if err != nil || got != 1 {
		t.Fatalf("retry canceled batch = (%d, %v), want (1, nil)", got, err)
	}
}

func TestDeleteStoppedNotificationsOperationTimeoutIntegration(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	collectorPool := openTestPool(ctx, t, postgresDSN(address, "notifier_collector"))
	store := postgres.NewStore(collectorPool, 100*time.Millisecond)
	if err := store.CheckConnection(ctx); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2025, time.November, 15, 12, 30, 0, 0, time.UTC)
	stopped := cutoff.Add(-time.Second)
	id := seedCollectionNotification(t, pool, "timed-out-batch", core.EventSuccess, core.DeliveryDelivered, &stopped)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if _, err := tx.Exec(ctx, "LOCK TABLE notifier.t_notification IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	count, err := store.DeleteStoppedNotifications(ctx, cutoff, 10)
	if count != 0 || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("timed-out collection = (%d, %v) after %v, want zero deleted and bounded operation deadline", count, err, time.Since(start))
	}
	if ctx.Err() != nil {
		t.Fatalf("parent context = %v, want only the database operation to time out", ctx.Err())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM notifier.t_notification WHERE id = $1)", id).Scan(&exists); err != nil || !exists {
		t.Fatalf("timed-out batch record exists = %t, %v, want true", exists, err)
	}
	count, err = store.DeleteStoppedNotifications(ctx, cutoff, 10)
	if err != nil || count != 1 {
		t.Fatalf("retry timed-out batch = (%d, %v), want (1, nil)", count, err)
	}
}

func collectionTestStore(t *testing.T) (*pgxpool.Pool, *postgres.Store) {
	t.Helper()
	pool := startPostgres(t)
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	collectorPool := openTestPool(t.Context(), t, postgresDSN(address, "notifier_collector"))
	return pool, postgres.NewStore(collectorPool, 5*time.Second)
}

func seedCollectionNotification(t *testing.T, pool *pgxpool.Pool, externalID, status, delivery string, stopped *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	start := time.Date(2023, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, err := pool.Exec(t.Context(), `INSERT INTO notifier.t_notification
        (id, client_id, external_id, data, start_dt, deadline_dt, next_scheduled_dt, stop_dt, notification_status, delivery_status)
        VALUES ($1, 'collector-fixture', $2, '[]', $3, $4, $3, $5, $6, NULLIF($7, ''))`,
		id, externalID, start, start.Add(time.Hour), stopped, status, delivery)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

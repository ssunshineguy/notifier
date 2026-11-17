//go:build integration

package postgres_test

import (
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestClaimNotificationsIntegration(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()
	poolConfig := pool.Config().ConnConfig
	address := net.JoinHostPort(poolConfig.Host, strconv.Itoa(int(poolConfig.Port)))
	workerPool := openTestPool(ctx, t, postgresDSN(address, "notifier_worker"))
	store := postgres.NewStore(workerPool, 2*time.Second)
	var now time.Time
	if err := pool.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
		t.Fatal(err)
	}

	seed := func(name string, scheduled, lockUntil *time.Time, stopped bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		var stoppedAt *time.Time
		notificationStatus, deliveryStatus := core.NotificationActive, ""
		if stopped {
			stoppedAt = &now
			notificationStatus, deliveryStatus = core.EventSuccess, core.DeliveryDelivered
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO notifier.t_notification
			    (id, client_id, external_id, data, start_dt, deadline_dt,
			     next_scheduled_dt, lock_until_dt, stop_dt, notification_status, delivery_status)
			VALUES ($1, 'worker-fixture', $2, '[]', $3, $4, $5, $6, $7, $8, NULLIF($9, ''))`,
			id, name, now.Add(-time.Hour), now.Add(time.Hour), scheduled, lockUntil, stoppedAt, notificationStatus, deliveryStatus)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	oldestTime := now.Add(-20 * time.Minute)
	olderTime := now.Add(-10 * time.Minute)
	newerTime := now.Add(-5 * time.Minute)
	future := now.Add(time.Hour)
	rowLocked := seed("row-locked", &oldestTime, nil, false)
	older := seed("older", &olderTime, &olderTime, false)
	newer := seed("newer", &newerTime, nil, false)
	seed("reserved", &olderTime, &future, false)
	seed("future", &future, nil, false)
	seed("stopped", &olderTime, nil, true)
	seed("unscheduled", nil, nil, false)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	var lockedID uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM notifier.t_notification WHERE id = $1 FOR UPDATE", rowLocked).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	claim := func(limit int, want ...uuid.UUID) []core.NotificationClaim {
		t.Helper()
		const lockDuration = 30 * time.Second
		claims, err := store.ClaimNotifications(ctx, limit, lockDuration)
		if err != nil {
			t.Fatal(err)
		}
		ids := make(map[uuid.UUID]bool, len(claims))
		for _, row := range claims {
			if ids[row.ID] {
				t.Fatalf("duplicate claim for %s", row.ID)
			}
			ids[row.ID] = true
			var persisted time.Time
			if err := pool.QueryRow(ctx, "SELECT lock_until_dt FROM notifier.t_notification WHERE id = $1", row.ID).Scan(&persisted); err != nil {
				t.Fatal(err)
			}
			if !persisted.Equal(row.LockUntil) || !persisted.After(now.Add(lockDuration)) {
				t.Errorf("claim = %+v, persisted lock = %v", row, persisted)
			}
		}
		wantIDs := make(map[uuid.UUID]bool, len(want))
		for _, id := range want {
			wantIDs[id] = true
		}
		if !reflect.DeepEqual(ids, wantIDs) {
			t.Fatalf("claimed %v, want %v", ids, wantIDs)
		}
		return claims
	}

	first := claim(1, older)[0]
	claim(2, newer)
	claim(10)

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	claim(1, rowLocked)
	if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET lock_until_dt = $1 WHERE id = $2", olderTime, older); err != nil {
		t.Fatal(err)
	}
	if next := claim(1, older)[0]; next.LockUntil.Before(first.LockUntil) {
		t.Errorf("new reservation deadline %v precedes previous deadline %v", next.LockUntil, first.LockUntil)
	}

	workerPool.Close()
	if claims, err := store.ClaimNotifications(ctx, 1, time.Second); err == nil || len(claims) != 0 {
		t.Fatalf("claim on closed pool = %v, %v", claims, err)
	}
}

//go:build integration

package postgres_test

import (
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestGetClaimedNotificationConditionsIntegration(t *testing.T) {
	pool := startPostgres(t)
	ctx := t.Context()
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	workerPool := openTestPool(ctx, t, postgresDSN(address, "notifier_worker"))
	store := postgres.NewStore(workerPool, time.Second)

	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	notification := core.Notification{
		ClientID: "worker-conditions", ExternalID: "test",
		Start: start, Deadline: start.Add(time.Hour), NextScheduled: start,
		Params: []core.Condition{{Type: core.ConditionAnd, Conditions: []core.Condition{
			{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: start.Add(time.Minute)}},
			{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: 9007199254740993}},
		}}},
	}
	id, err := store.CreateNotification(ctx, notification)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.ClaimNotifications(ctx, 1, 30*time.Second)
	if err != nil || len(claims) != 1 {
		t.Fatalf("ClaimNotifications = %+v, %v, want one claim", claims, err)
	}
	claim := claims[0]
	if claim.ID != id {
		t.Fatalf("claim ID = %v, want %v", claim.ID, id)
	}
	if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET failed_attempt_count = 2 WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetClaimedNotificationConditions(ctx, claim)
	if err != nil || got.ClientID != notification.ClientID || got.ExternalID != notification.ExternalID ||
		!got.Deadline.Equal(notification.Deadline) || got.NotificationStatus != core.NotificationActive ||
		got.FailedAttemptCount != 2 || !reflect.DeepEqual(got.Params, notification.Params) {
		t.Fatalf("GetClaimedNotificationConditions = %+v, %v, want client and nested conditions", got, err)
	}

	assertUnavailable := func(claim core.NotificationClaim) {
		t.Helper()
		if _, err := store.GetClaimedNotificationConditions(ctx, claim); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Errorf("GetClaimedNotificationConditions = %v, want unavailable claim", err)
		}
	}
	stale := claim
	stale.LockUntil = claim.LockUntil.Add(-time.Second)
	assertUnavailable(stale)

	if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET data = '{}' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetClaimedNotificationConditions(ctx, claim); err == nil || errors.Is(err, core.ErrNotificationNotFound) {
		t.Errorf("GetClaimedNotificationConditions with invalid JSON shape = %v, want decode error", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET lock_until_dt = $1 WHERE id = $2", start, id); err != nil {
		t.Fatal(err)
	}
	assertUnavailable(core.NotificationClaim{ID: id, LockUntil: start})

	if _, err := pool.Exec(ctx, `UPDATE notifier.t_notification
		SET lock_until_dt = $1, stop_dt = now(), notification_status = 'success', delivery_status = 'delivered' WHERE id = $2`, claim.LockUntil, id); err != nil {
		t.Fatal(err)
	}
	assertUnavailable(claim)
}

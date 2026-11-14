//go:build integration

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestCreateNotificationIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 2*time.Second)
	ctx := t.Context()

	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	after := start.Add(time.Minute)
	notification := core.Notification{
		ClientID:      "owner",
		ExternalID:    "same-external-id",
		Start:         start,
		Deadline:      start.Add(time.Hour),
		NextScheduled: start,
		Params:        []core.Condition{{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}}},
	}
	id, err := store.CreateNotification(ctx, notification)
	if err != nil {
		t.Fatal(err)
	}
	if id.Version() != 7 {
		t.Fatalf("notification ID = %s, want UUID v7", id)
	}

	retry := notification
	retry.Params = []core.Condition{{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after.Add(time.Minute)}}}
	_, err = store.CreateNotification(ctx, retry)
	var duplicate *core.AlreadyExistsError
	if !errors.As(err, &duplicate) || duplicate.ID != id {
		t.Fatalf("retry = %v, want AlreadyExistsError with ID %s", err, id)
	}

	stored, err := store.GetNotification(ctx, id, notification.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Params) != 1 || !stored.Params[0].Time.Threshold.Equal(after) {
		t.Fatalf("retry changed the stored conditions: %+v", stored.Params)
	}

	other := notification
	other.ClientID = "other"
	otherID, err := store.CreateNotification(ctx, other)
	if err != nil || otherID == uuid.Nil || otherID == id {
		t.Fatalf("create for another client = (%s, %v), want a separate notification", otherID, err)
	}

	if _, err := pool.Exec(ctx, "UPDATE notifier.t_notification SET stop_dt = now(), stop_reason = 'delivered' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	for clientID, wantID := range map[string]uuid.UUID{notification.ClientID: id, other.ClientID: otherID} {
		gotID, found, err := store.FindNotificationID(ctx, clientID, notification.ExternalID)
		if err != nil || !found || gotID != wantID {
			t.Errorf("lookup for %s = (%s, %t, %v), want %s", clientID, gotID, found, err, wantID)
		}
	}
	gotID, found, err := store.FindNotificationID(ctx, "missing", notification.ExternalID)
	if err != nil || found || gotID != uuid.Nil {
		t.Errorf("lookup for missing client = (%s, %t, %v), want no match", gotID, found, err)
	}

	invalid := notification
	invalid.ExternalID = "invalid-deadline"
	invalid.Deadline = invalid.Start
	_, err = store.CreateNotification(ctx, invalid)
	var dbError *pgconn.PgError
	if !errors.As(err, &dbError) || dbError.ConstraintName != "ck_notification_deadline_dt_start_dt" {
		t.Errorf("create with invalid deadline = %v, want deadline constraint failure", err)
	}

	pool.Close()
	_, found, err = store.FindNotificationID(ctx, notification.ClientID, notification.ExternalID)
	if err == nil || found {
		t.Errorf("lookup on a closed pool = (%t, %v), want storage error", found, err)
	}
}

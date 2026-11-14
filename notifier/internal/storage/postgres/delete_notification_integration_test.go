//go:build integration

package postgres_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

func TestDeleteNotificationIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 5*time.Second)
	const owner = "delete-owner"
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	scheduled := start.Add(time.Minute)
	conditions := []core.Condition{{Type: core.ConditionTime, After: start.Add(2 * time.Minute)}}

	create := func(t *testing.T, externalID string) uuid.UUID {
		t.Helper()
		id, err := store.CreateNotification(t.Context(), core.Notification{
			ClientID: owner, ExternalID: externalID, Params: conditions,
			Start: start, Deadline: start.Add(time.Hour), NextScheduled: scheduled,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	exists := func(t *testing.T, id uuid.UUID) bool {
		t.Helper()
		var found bool
		err := pool.QueryRow(t.Context(),
			"SELECT EXISTS (SELECT 1 FROM notifier.t_notification WHERE id = $1)", id).Scan(&found)
		if err != nil {
			t.Fatal(err)
		}
		return found
	}

	t.Run("foreign and missing", func(t *testing.T) {
		id := create(t, "foreign")
		if err := store.DeleteNotification(t.Context(), id, "other-client"); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Fatalf("foreign DELETE = %v, want ErrNotificationNotFound", err)
		}
		if err := store.DeleteNotification(t.Context(), uuid.New(), owner); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Fatalf("missing DELETE = %v, want ErrNotificationNotFound", err)
		}
		if !exists(t, id) {
			t.Fatal("foreign DELETE removed the owner's notification")
		}
	})

	for _, status := range []string{"active", "delivered", "deadline_exceeded", "delivery_failed", "deleted"} {
		t.Run(status, func(t *testing.T) {
			ctx := t.Context()
			id := create(t, status)
			if status != "active" {
				if _, err := pool.Exec(ctx, `
					UPDATE notifier.t_notification
					SET stop_dt = $1, stop_reason = $2, next_scheduled_dt = NULL
					WHERE id = $3`, scheduled, status, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.DeleteNotification(ctx, id, owner); err != nil {
				t.Fatal(err)
			}
			if exists(t, id) {
				t.Fatal("DELETE left a row in PostgreSQL")
			}
			if err := store.DeleteNotification(ctx, id, owner); !errors.Is(err, core.ErrNotificationNotFound) {
				t.Fatalf("repeated DELETE = %v, want ErrNotificationNotFound", err)
			}
			if _, err := store.GetNotification(ctx, id, owner); !errors.Is(err, core.ErrNotificationNotFound) {
				t.Fatalf("GET after DELETE = %v, want ErrNotificationNotFound", err)
			}
			if _, err := store.UpdateNotification(ctx, id, owner, conditions); !errors.Is(err, core.ErrNotificationNotFound) {
				t.Fatalf("PUT after DELETE = %v, want ErrNotificationNotFound", err)
			}
			if nextID := create(t, status); nextID == id {
				t.Fatal("reusing the external ID returned the deleted notification ID")
			}
		})
	}

	t.Run("database error", func(t *testing.T) {
		pool.Close()
		err := store.DeleteNotification(t.Context(), uuid.New(), owner)
		if err == nil || !strings.HasPrefix(err.Error(), "delete notification:") {
			t.Fatalf("DELETE = %v, want storage error", err)
		}
	})
}

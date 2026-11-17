//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"math"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/application"
	"github.com/ssunshineguy/notifier/notifier/internal/config"
	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
)

type notificationState struct {
	name, notificationStatus, deliveryStatus string
}

var stoppedNotificationStates = [...]notificationState{
	{"condition event delivered", core.EventSuccess, core.DeliveryDelivered},
	{"deadline event delivered", core.EventDeadlineExceeded, core.DeliveryDelivered},
	{"delivery exhausted", core.EventSuccess, core.DeliveryFailed},
	{"legacy deleted", core.NotificationActive, ""},
}

func TestGetNotificationIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 2*time.Second)
	ctx := t.Context()

	const owner = "owner"
	const externalID = "integration-read"
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	after := start.Add(time.Hour)
	deadline := after.Add(time.Hour)
	conditions := []core.Condition{{Type: core.ConditionAnd, Conditions: []core.Condition{
		{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
		{Type: core.ConditionOr, Conditions: []core.Condition{
			{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.GreaterThan, Threshold: after}},
			{Type: core.ConditionTime, Time: core.TimeCondition{Operation: core.LessThan, Threshold: after.Add(time.Minute)}},
			{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.LessThan, Threshold: math.MaxInt64}},
		}},
		{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 0}},
	}}}
	id, err := store.CreateNotification(ctx, core.Notification{
		ClientID:      owner,
		ExternalID:    externalID,
		Params:        conditions,
		Start:         start,
		Deadline:      deadline,
		NextScheduled: start,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertStatus := func(t *testing.T, notificationStatus, deliveryStatus string) {
		t.Helper()
		got, err := store.GetNotification(t.Context(), id, owner)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != id || got.ExternalID != externalID || got.NotificationStatus != notificationStatus || got.DeliveryStatus != deliveryStatus ||
			!got.Start.Equal(start) || !got.Deadline.Equal(deadline) || !reflect.DeepEqual(got.Params, conditions) {
			t.Fatalf("unexpected notification for %q / %q: %+v", notificationStatus, deliveryStatus, got)
		}
	}
	assertMissing := func(t *testing.T, notificationID uuid.UUID, clientID string) {
		t.Helper()
		if _, err := store.GetNotification(t.Context(), notificationID, clientID); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Fatalf("GetNotification = %v, want ErrNotificationNotFound", err)
		}
	}

	t.Run("active", func(t *testing.T) { assertStatus(t, core.NotificationActive, "") })
	t.Run("foreign owner and unknown ID", func(t *testing.T) {
		assertMissing(t, id, "other-client")
		assertMissing(t, uuid.New(), owner)
	})
	for _, state := range stoppedNotificationStates {
		t.Run(state.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), `
				UPDATE notifier.t_notification
				SET stop_dt = now(), notification_status = $1, delivery_status = NULLIF($2, '')
				WHERE id = $3`, state.notificationStatus, state.deliveryStatus, id)
			if err != nil {
				t.Fatal(err)
			}
			assertStatus(t, state.notificationStatus, state.deliveryStatus)
			assertMissing(t, id, "other-client")
		})
	}

	for _, fixture := range []struct {
		name    string
		data    string
		wantErr string
	}{
		{"unknown nested condition", `[{"type":"and","conditions":[{}]}]`, "map notification params:"},
		{"time without timestamp", `[{"type":"time","operation":"greater_than"}]`, "map notification params:"},
		{"invalid timestamp", `[{"type":"time","operation":"greater_than","value":"invalid"}]`, "map notification params:"},
		{"unencodable time offset", `[{"type":"time","operation":"greater_than","value":"2026-12-02T09:00:00+24:00"}]`, "map notification params:"},
		{"invalid time operation", `[{"type":"time","operation":"equal","value":"2026-12-02T09:00:00Z"}]`, "map notification params:"},
		{"price without threshold", `[{"type":"price","operation":"less_than"}]`, "map notification params:"},
		{"null price threshold", `[{"type":"price","operation":"less_than","value":null}]`, "map notification params:"},
		{"string price threshold", `[{"type":"price","operation":"less_than","value":"0"}]`, "map notification params:"},
		{"negative price threshold", `[{"type":"price","operation":"greater_than","value":-1}]`, "map notification params:"},
		{"invalid price operation", `[{"type":"price","operation":"equal","value":0}]`, "map notification params:"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), "UPDATE notifier.t_notification SET data = $1::jsonb WHERE id = $2", fixture.data, id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.GetNotification(t.Context(), id, owner)
			if err == nil || !strings.HasPrefix(err.Error(), fixture.wantErr) {
				t.Fatalf("GetNotification = %v, want error starting with %q", err, fixture.wantErr)
			}
		})
	}

	t.Run("removed record", func(t *testing.T) {
		if _, err := pool.Exec(t.Context(), "DELETE FROM notifier.t_notification WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
		assertMissing(t, id, owner)
	})
	t.Run("database error", func(t *testing.T) {
		pool.Close()
		_, err := store.GetNotification(t.Context(), id, owner)
		if err == nil || !strings.HasPrefix(err.Error(), "query notification:") {
			t.Fatalf("GetNotification = %v, want query error", err)
		}
	})
}

func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	initDir, err := filepath.Abs("../../../db/init")
	if err != nil {
		t.Fatal(err)
	}
	name := "notifier-integration-" + uuid.NewString()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "docker", "rm", "-f", "-v", name).CombinedOutput() //nolint:gosec // The name is generated by this test.
		if err != nil && !strings.Contains(string(output), "No such container") {
			t.Errorf("remove test PostgreSQL: %v\n%s", err, output)
		}
	})
	dockerOutput(ctx, t, "run", "-d", "--name", name, "-p", "127.0.0.1::5432",
		"-e", "POSTGRES_PASSWORD=integration-only", "-e", "POSTGRES_DB=notifier",
		"-v", initDir+":/docker-entrypoint-initdb.d:ro", "postgres:18-alpine")
	addr := dockerOutput(ctx, t, "port", name, "5432/tcp")

	migratorDSN := postgresDSN(addr, "notifier_migrator")
	migrator := openTestPool(ctx, t, migratorDSN)
	waitForPostgres(ctx, t, migrator)
	if err := application.Migrate(ctx, config.Config{Database: config.Database{DSN: migratorDSN}}); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(ctx, t, postgresDSN(addr, "notifier_api"))
	if err := postgres.NewStore(pool, 2*time.Second).CheckConnection(ctx); err != nil {
		t.Fatalf("check API connection: %v", err)
	}
	return pool
}

func dockerOutput(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput() //nolint:gosec // Arguments come from the test fixture.
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, output)
	}
	return strings.TrimSpace(string(output))
}

func postgresDSN(addr, role string) string {
	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(role, role),
		Host:     addr,
		Path:     "/notifier",
		RawQuery: "sslmode=disable",
	}
	return dsn.String()
}

func openTestPool(ctx context.Context, t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func waitForPostgres(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	store := postgres.NewStore(pool, time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := store.CheckConnection(ctx)
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for PostgreSQL: %v", err)
		case <-ticker.C:
		}
	}
}

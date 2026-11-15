//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ssunshineguy/notifier/notifier/internal/core"
	"github.com/ssunshineguy/notifier/notifier/internal/storage/postgres"
	"github.com/ssunshineguy/notifier/notifier/internal/worker"
)

type dlqStoredState struct {
	Client, External, Data, Status, Delivery, Reason, Snapshot string
	Start, Deadline                                            time.Time
	Next, Lease, Stop, DLQAt                                   *time.Time
	Attempts                                                   int
}

func readDLQState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) dlqStoredState {
	t.Helper()
	var state dlqStoredState
	err := pool.QueryRow(t.Context(), `SELECT notification.client_id, notification.external_id, notification.data::text,
		notification.notification_status, COALESCE(notification.delivery_status, ''), COALESCE(dlq.reason, ''),
		COALESCE(dlq.notification::text, ''), notification.start_dt, notification.deadline_dt,
		notification.next_scheduled_dt, notification.lock_until_dt, notification.stop_dt, dlq.created_dt,
		notification.failed_attempt_count
		FROM notifier.t_notification AS notification
		LEFT JOIN notifier.t_dlq AS dlq ON dlq.notification_id = notification.id
		WHERE notification.id = $1`, id).Scan(
		&state.Client, &state.External, &state.Data, &state.Status, &state.Delivery, &state.Reason, &state.Snapshot,
		&state.Start, &state.Deadline, &state.Next, &state.Lease, &state.Stop, &state.DLQAt, &state.Attempts)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertDLQArchiveCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM notifier.t_dlq WHERE notification_id = $1", id).Scan(&count); err != nil || count != want {
		t.Fatalf("DLQ archive count = %d, %v; want %d", count, err, want)
	}
}

func newDLQStore(t *testing.T, pool *pgxpool.Pool, timeout time.Duration) *postgres.Store {
	t.Helper()
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	return postgres.NewStore(openTestPool(t.Context(), t, postgresDSN(address, "notifier_worker")), timeout)
}

func createDLQFixture(t *testing.T, store *postgres.Store, client, external string, expired bool) uuid.UUID {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	deadline := now.Add(time.Hour)
	if expired {
		deadline = now.Add(-time.Minute)
	}
	id, err := store.CreateNotification(t.Context(), core.Notification{
		ClientID: client, ExternalID: external, Start: now.Add(-time.Hour), Deadline: deadline,
		NextScheduled: now.Add(-time.Second),
		Params:        []core.Condition{{Type: core.ConditionPrice, Price: core.PriceCondition{Operation: core.GreaterThan, Threshold: 10}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func reserveDLQFixture(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, duration time.Duration) core.NotificationClaim {
	t.Helper()
	claim := core.NotificationClaim{ID: id}
	if err := pool.QueryRow(t.Context(), `UPDATE notifier.t_notification
		SET lock_until_dt = clock_timestamp() + $2::bigint * interval '1 millisecond'
		WHERE id = $1 RETURNING lock_until_dt`, id, duration.Milliseconds()).Scan(&claim.LockUntil); err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestMoveNotificationToDLQIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := newDLQStore(t, pool, 2*time.Second)
	const owner = "dlq-owner"
	id := createDLQFixture(t, store, owner, "preserved", false)
	if _, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification
		SET data = '{"damaged":"conditions"}', failed_attempt_count = 7 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	claim := reserveDLQFixture(t, pool, id, time.Minute)
	before := readDLQState(t, pool, id)
	var originalSnapshot string
	if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(notification)::text FROM notifier.t_notification AS notification WHERE id = $1", id).Scan(&originalSnapshot); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	if err := pool.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&started); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveNotificationToDLQ(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	after := readDLQState(t, pool, id)
	var finished time.Time
	if err := pool.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if after.Stop == nil || after.DLQAt == nil || !after.Stop.Equal(*after.DLQAt) ||
		after.DLQAt.Before(started) || after.DLQAt.After(finished) {
		t.Fatalf("DLQ timestamps = stop %v, dlq %v", after.Stop, after.DLQAt)
	}
	want := before
	want.Status, want.Delivery, want.Reason = core.NotificationDLQ, "", core.DLQInvalidStoredConditions
	want.Snapshot = originalSnapshot
	want.Next, want.Lease, want.Stop, want.DLQAt = nil, nil, after.Stop, after.DLQAt
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("DLQ state = %+v, want %+v", after, want)
	}
	if err := store.MoveNotificationToDLQ(t.Context(), claim); !errors.Is(err, core.ErrNotificationNotFound) {
		t.Fatalf("repeated transition = %v, want unavailable claim", err)
	}
	if repeated := readDLQState(t, pool, id); !reflect.DeepEqual(repeated, after) {
		t.Fatalf("repeated transition changed state: %+v", repeated)
	}
	assertDLQArchiveCount(t, pool, id, 1)
	claims, err := store.ClaimNotifications(t.Context(), 10, time.Minute)
	if err != nil || len(claims) != 0 {
		t.Fatalf("claims after DLQ = %+v, %v, want none", claims, err)
	}

	details, err := store.GetNotification(t.Context(), id, owner)
	if err != nil || details.ID != id || details.ExternalID != before.External ||
		details.NotificationStatus != core.NotificationDLQ || details.DeliveryStatus != "" || len(details.Params) != 0 ||
		details.DLQReason != core.DLQInvalidStoredConditions || !sameTimestamp(details.Stop, after.Stop) ||
		!sameTimestamp(details.DLQAt, after.DLQAt) || !details.Start.Equal(before.Start) || !details.Deadline.Equal(before.Deadline) {
		t.Fatalf("GET damaged DLQ = %+v, %v", details, err)
	}
	if _, err := store.GetNotification(t.Context(), id, "foreign"); !errors.Is(err, core.ErrNotificationNotFound) {
		t.Fatalf("foreign GET = %v", err)
	}
	if _, err := store.UpdateNotification(t.Context(), id, owner, nil); !errors.Is(err, core.ErrNotificationStopped) {
		t.Fatalf("DLQ PUT = %v, want stopped", err)
	}
	if _, err := store.UpdateNotification(t.Context(), id, "foreign", nil); !errors.Is(err, core.ErrNotificationNotFound) {
		t.Fatalf("foreign PUT = %v", err)
	}
	if err := store.DeleteNotification(t.Context(), id, "foreign"); !errors.Is(err, core.ErrNotificationNotFound) {
		t.Fatalf("foreign DELETE = %v", err)
	}
	duplicate := core.Notification{ClientID: owner, ExternalID: before.External, Start: before.Start, Deadline: before.Deadline, NextScheduled: before.Start}
	_, err = store.CreateNotification(t.Context(), duplicate)
	var exists *core.AlreadyExistsError
	if !errors.As(err, &exists) || exists.ID != id {
		t.Fatalf("duplicate of DLQ = %v, want original UUID", err)
	}
	if got, found, err := store.FindNotificationID(t.Context(), owner, before.External); err != nil || !found || got != id {
		t.Fatalf("find DLQ = %v, %t, %v", got, found, err)
	}

	for _, tc := range []struct{ name, mutation string }{
		{"delivery", "delivery_status = 'delivered'"},
		{"missing stop", "stop_dt = NULL"},
		{"schedule", "next_scheduled_dt = clock_timestamp()"},
		{"lease", "lock_until_dt = clock_timestamp()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), "UPDATE notifier.t_notification SET "+tc.mutation+" WHERE id = $1", id)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("invalid DLQ state error = %v, want check violation", err)
			}
			if got := readDLQState(t, pool, id); !reflect.DeepEqual(got, after) {
				t.Fatalf("rejected update changed state: %+v", got)
			}
		})
	}
	for _, tc := range []struct{ name, mutation, code string }{
		{"archive missing ID", "notification_id = NULL", "23502"},
		{"archive missing snapshot", "notification = NULL", "23502"},
		{"archive non-object snapshot", "notification = '[]'::jsonb", "23514"},
		{"archive missing reason", "reason = NULL", "23502"},
		{"archive unknown reason", "reason = 'unknown'", "23514"},
		{"archive missing timestamp", "created_dt = NULL", "23502"},
		{"archive missing parent", "notification_id = '00000000-0000-0000-0000-000000000000'", "23503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), "UPDATE notifier.t_dlq SET "+tc.mutation+" WHERE notification_id = $1", id)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
				t.Fatalf("invalid DLQ archive error = %v, want SQLSTATE %s", err, tc.code)
			}
			if got := readDLQState(t, pool, id); !reflect.DeepEqual(got, after) {
				t.Fatalf("rejected archive update changed state: %+v", got)
			}
		})
	}
	t.Run("archive primary key", func(t *testing.T) {
		_, err := pool.Exec(t.Context(), `INSERT INTO notifier.t_dlq (notification_id, notification, reason, created_dt)
			SELECT notification_id, notification, reason, created_dt FROM notifier.t_dlq WHERE notification_id = $1`, id)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("duplicate archive = %v, want unique violation", err)
		}
		assertDLQArchiveCount(t, pool, id, 1)
	})
	if err := store.DeleteNotification(t.Context(), id, owner); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM notifier.t_notification WHERE id = $1", id).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("DLQ records after DELETE = %d, %v", remaining, err)
	}
	assertDLQArchiveCount(t, pool, id, 0)
	if replacement, err := store.CreateNotification(t.Context(), duplicate); err != nil || replacement == id {
		t.Fatalf("recreate external ID after DELETE = %v, %v", replacement, err)
	}
}

func TestDLQArchiveAtomicityAndCascadeIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := newDLQStore(t, pool, 2*time.Second)
	conn := pool.Config().ConnConfig
	address := net.JoinHostPort(conn.Host, strconv.Itoa(int(conn.Port)))
	migrator := openTestPool(t.Context(), t, postgresDSN(address, "notifier_migrator"))
	id := createDLQFixture(t, store, "atomic-owner", "archive-failure", false)
	if _, err := pool.Exec(t.Context(), "UPDATE notifier.t_notification SET data = '{}' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	claim := reserveDLQFixture(t, pool, id, time.Minute)
	before := readDLQState(t, pool, id)
	if _, err := migrator.Exec(t.Context(), "ALTER TABLE notifier.t_dlq ADD CONSTRAINT ck_test_reject_archive CHECK (false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	err := store.MoveNotificationToDLQ(t.Context(), claim)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("archive insert failure = %v, want injected check violation", err)
	}
	if after := readDLQState(t, pool, id); !reflect.DeepEqual(before, after) {
		t.Fatalf("archive insert failure changed main row: before %+v, after %+v", before, after)
	}
	assertDLQArchiveCount(t, pool, id, 0)
	if _, err := migrator.Exec(t.Context(), "ALTER TABLE notifier.t_dlq DROP CONSTRAINT ck_test_reject_archive"); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveNotificationToDLQ(t.Context(), claim); err != nil {
		t.Fatalf("retry transition after archive recovery: %v", err)
	}
	assertDLQArchiveCount(t, pool, id, 1)
	if _, err := pool.Exec(t.Context(), "DELETE FROM notifier.t_notification WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	assertDLQArchiveCount(t, pool, id, 0)
	if _, err := store.GetNotification(t.Context(), id, "atomic-owner"); !errors.Is(err, core.ErrNotificationNotFound) {
		t.Fatalf("parent SQL deletion retained notification: %v", err)
	}
	createDLQFixture(t, store, "atomic-owner", "archive-failure", false)
}

func TestDLQAndRescheduleRejectUnavailableClaimsIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := newDLQStore(t, pool, 2*time.Second)
	for _, operation := range []struct {
		name string
		run  func(context.Context, core.NotificationClaim) error
	}{
		{"dlq", store.MoveNotificationToDLQ},
		{"reschedule", func(ctx context.Context, claim core.NotificationClaim) error {
			return store.RescheduleNotification(ctx, claim, time.Now().Add(time.Minute))
		}},
	} {
		for _, state := range []string{"expired", "reclaimed", "stopped", "deleted"} {
			t.Run(operation.name+"/"+state, func(t *testing.T) {
				id := createDLQFixture(t, store, "claim-owner", operation.name+state, false)
				claim := reserveDLQFixture(t, pool, id, time.Minute)
				switch state {
				case "expired":
					claim = reserveDLQFixture(t, pool, id, -time.Second)
				case "reclaimed":
					reserveDLQFixture(t, pool, id, 2*time.Minute)
				case "stopped":
					if _, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification SET stop_dt = clock_timestamp(),
						notification_status = 'success', delivery_status = 'delivered' WHERE id = $1`, id); err != nil {
						t.Fatal(err)
					}
				case "deleted":
					if _, err := pool.Exec(t.Context(), "DELETE FROM notifier.t_notification WHERE id = $1", id); err != nil {
						t.Fatal(err)
					}
				}
				var before dlqStoredState
				if state != "deleted" {
					before = readDLQState(t, pool, id)
				}
				if err := operation.run(t.Context(), claim); !errors.Is(err, core.ErrNotificationNotFound) {
					t.Fatalf("unavailable claim mutation = %v", err)
				}
				assertDLQArchiveCount(t, pool, id, 0)
				if state != "deleted" {
					if after := readDLQState(t, pool, id); !reflect.DeepEqual(before, after) {
						t.Fatalf("lost claim changed state: before %+v, after %+v", before, after)
					}
				} else if _, err := store.GetNotification(t.Context(), id, "claim-owner"); !errors.Is(err, core.ErrNotificationNotFound) {
					t.Fatalf("deleted notification reappeared: %v", err)
				}
			})
		}
	}
	t.Run("selected event cannot enter DLQ", func(t *testing.T) {
		id := createDLQFixture(t, store, "claim-owner", "selected", false)
		claim := reserveDLQFixture(t, pool, id, time.Minute)
		if _, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification
			SET notification_status = 'success', delivery_status = 'trying_to_deliver' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		before := readDLQState(t, pool, id)
		if err := store.MoveNotificationToDLQ(t.Context(), claim); !errors.Is(err, core.ErrNotificationNotFound) {
			t.Fatalf("selected event DLQ = %v", err)
		}
		if after := readDLQState(t, pool, id); !reflect.DeepEqual(before, after) {
			t.Fatalf("selected event changed: %+v", after)
		}
		assertDLQArchiveCount(t, pool, id, 0)
	})
}

func TestTechnicalReschedulePreservesStateIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := newDLQStore(t, pool, 2*time.Second)
	for _, status := range []string{core.NotificationActive, core.EventSuccess, core.EventDeadlineExceeded} {
		t.Run(status, func(t *testing.T) {
			id := createDLQFixture(t, store, "retry-owner", status, true)
			delivery := ""
			if status != core.NotificationActive {
				delivery = core.DeliveryTryingToDeliver
			}
			if _, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification SET data = '{}',
				notification_status = $2, delivery_status = NULLIF($3, ''), failed_attempt_count = 3 WHERE id = $1`, id, status, delivery); err != nil {
				t.Fatal(err)
			}
			claim := reserveDLQFixture(t, pool, id, time.Minute)
			before := readDLQState(t, pool, id)
			next := time.Now().UTC().Truncate(time.Microsecond).Add(10 * time.Second)
			if err := store.RescheduleNotification(t.Context(), claim, next); err != nil {
				t.Fatal(err)
			}
			want := before
			after := readDLQState(t, pool, id)
			want.Next, want.Lease = after.Next, nil
			if !sameTimestamp(after.Next, &next) || !reflect.DeepEqual(after, want) || !after.Next.After(time.Now()) || !after.Next.After(after.Deadline) {
				t.Fatalf("technical retry state = %+v, want %+v", after, want)
			}
		})
	}
	if claims, err := store.ClaimNotifications(t.Context(), 10, time.Minute); err != nil || len(claims) != 0 {
		t.Fatalf("technical retries immediately claimable = %+v, %v", claims, err)
	}
}

func TestDLQMutationsRejectLeaseExpiringAtRowLockIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := newDLQStore(t, pool, 5*time.Second)
	for _, operation := range []struct {
		name string
		run  func(context.Context, core.NotificationClaim) error
	}{
		{"dlq", store.MoveNotificationToDLQ},
		{"reschedule", func(ctx context.Context, claim core.NotificationClaim) error {
			return store.RescheduleNotification(ctx, claim, time.Now().Add(time.Minute))
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
			defer cancel()
			id := createDLQFixture(t, store, "blocked-owner", operation.name, false)
			claim := reserveDLQFixture(t, pool, id, 2*time.Second)
			before := readDLQState(t, pool, id)
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
			go func() { done <- operation.run(ctx, claim) }()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting, unexpired bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))),
					clock_timestamp() < $2`, tx.Conn().PgConn().PID(), claim.LockUntil).Scan(&waiting, &unexpired); err != nil {
					t.Fatal(err)
				}
				if !unexpired {
					t.Fatal("mutation did not reach row lock before lease expiry")
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("mutation completed while row locked: %v", err)
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
				t.Fatalf("lease expired while blocked: %v, want unavailable claim", err)
			}
			if after := readDLQState(t, pool, id); !reflect.DeepEqual(before, after) {
				t.Fatalf("expired claim mutation changed state: before %+v, after %+v", before, after)
			}
			assertDLQArchiveCount(t, pool, id, 0)
		})
	}
}

type dlqCallback struct {
	events []core.NotificationEvent
	err    error
}

func (callback *dlqCallback) Send(_ context.Context, _, _ string, event core.NotificationEvent) (int, error) {
	callback.events = append(callback.events, event)
	if callback.err != nil {
		return http.StatusServiceUnavailable, callback.err
	}
	return http.StatusOK, nil
}

type dlqInformationService struct{ calls int }

func (source *dlqInformationService) GetTime(context.Context, string, string) (time.Time, error) {
	source.calls++
	return time.Now(), nil
}

func (source *dlqInformationService) GetPrice(context.Context, string, string) (int64, error) {
	source.calls++
	return 20, nil
}

func TestDLQProcessorIntegration(t *testing.T) {
	pool := startPostgres(t)
	store := newDLQStore(t, pool, 2*time.Second)
	settings := worker.ProcessingSettings{ScheduledDelay: time.Second, TechnicalRetryDelay: 10 * time.Second, ResultWriteTimeout: time.Second}
	if _, err := pool.Exec(t.Context(), `INSERT INTO notifier.t_client (client_id, callback_url, max_lifetime_sec)
		VALUES ('processor-owner', 'http://example.test/callback', 7200)`); err != nil {
		t.Fatal(err)
	}
	t.Run("broken conditions stop without callback and healthy rows continue", func(t *testing.T) {
		broken := createDLQFixture(t, store, "processor-owner", "broken", false)
		healthy := createDLQFixture(t, store, "processor-owner", "healthy", false)
		if _, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification
			SET data = '[{"type":"and","conditions":[{"type":"unknown"}]}]', failed_attempt_count = 2 WHERE id = $1`, broken); err != nil {
			t.Fatal(err)
		}
		claims, err := store.ClaimNotifications(t.Context(), 2, time.Minute)
		if err != nil || len(claims) != 2 {
			t.Fatalf("claim broken and healthy = %+v, %v", claims, err)
		}
		callback := &dlqCallback{}
		source := &dlqInformationService{}
		for _, claim := range claims {
			worker.ProcessNotification(t.Context(), claim, store, source, callback, settings)
		}
		if len(callback.events) != 1 || callback.events[0].NotificationID != healthy || callback.events[0].Status != core.EventSuccess || source.calls != 1 {
			t.Fatalf("callbacks = %+v, source calls = %d, want healthy success only", callback.events, source.calls)
		}
		if state := readDLQState(t, pool, broken); state.Status != core.NotificationDLQ || state.Attempts != 2 || state.Delivery != "" || state.Next != nil || state.Lease != nil || state.DLQAt == nil {
			t.Fatalf("broken processing state = %+v", state)
		}
		if state := readDLQState(t, pool, healthy); state.Status != core.EventSuccess || state.Delivery != core.DeliveryDelivered || state.DLQAt != nil || state.Reason != "" {
			t.Fatalf("healthy processing state = %+v", state)
		}
	})
	for _, malformed := range []struct{ name, data string }{
		{"object", `{}`},
		{"null", `null`},
		{"empty root", `[]`},
		{"empty group", `[{"type":"and","conditions":[]}]`},
		{"one child group", `[{"type":"or","conditions":[{"type":"price","operation":"less_than","value":10}]}]`},
		{"invalid price", `[{"type":"price","operation":"greater_than","value":-1}]`},
		{"invalid timestamp", `[{"type":"time","operation":"greater_than","value":"invalid"}]`},
	} {
		t.Run("malformed/"+malformed.name, func(t *testing.T) {
			id := createDLQFixture(t, store, "processor-owner", "malformed/"+malformed.name, false)
			if _, err := pool.Exec(t.Context(), "UPDATE notifier.t_notification SET data = $2::jsonb WHERE id = $1", id, malformed.data); err != nil {
				t.Fatal(err)
			}
			claim := reserveDLQFixture(t, pool, id, time.Minute)
			if _, err := store.GetClaimedNotificationConditions(t.Context(), claim); !errors.Is(err, core.ErrInvalidStoredConditions) {
				t.Fatalf("malformed conditions error = %v, want permanent classification", err)
			}
			callback := &dlqCallback{}
			source := &dlqInformationService{}
			worker.ProcessNotification(t.Context(), claim, store, source, callback, settings)
			state := readDLQState(t, pool, id)
			if len(callback.events) != 0 || source.calls != 0 || state.Status != core.NotificationDLQ || state.Attempts != 0 {
				t.Fatalf("malformed processing: events %+v, source calls %d, state %+v", callback.events, source.calls, state)
			}
		})
	}
	for _, tc := range []struct {
		name, status, wantStatus string
		attempts                 int
		rejected                 bool
	}{
		{"selected success", core.EventSuccess, core.EventSuccess, 1, false},
		{"selected deadline", core.EventDeadlineExceeded, core.EventDeadlineExceeded, 1, false},
		{"expired active", core.NotificationActive, core.EventDeadlineExceeded, 0, false},
		{"callback exhaustion", core.EventSuccess, core.EventSuccess, 41, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := createDLQFixture(t, store, "processor-owner", tc.name, true)
			delivery := ""
			if tc.status != core.NotificationActive {
				delivery = core.DeliveryTryingToDeliver
			}
			if _, err := pool.Exec(t.Context(), `UPDATE notifier.t_notification SET data = '{}', notification_status = $2,
				delivery_status = NULLIF($3, ''), failed_attempt_count = $4 WHERE id = $1`, id, tc.status, delivery, tc.attempts); err != nil {
				t.Fatal(err)
			}
			claim := reserveDLQFixture(t, pool, id, time.Minute)
			callback := &dlqCallback{}
			if tc.rejected {
				callback.err = errors.New("receiver unavailable")
			}
			source := &dlqInformationService{}
			worker.ProcessNotification(t.Context(), claim, store, source, callback, settings)
			wantDelivery, wantAttempts := core.DeliveryDelivered, tc.attempts
			if tc.rejected {
				wantDelivery, wantAttempts = core.DeliveryFailed, 42
			}
			state := readDLQState(t, pool, id)
			if len(callback.events) != 1 || callback.events[0].Status != tc.wantStatus || source.calls != 0 ||
				state.Status != tc.wantStatus || state.Delivery != wantDelivery || state.Attempts != wantAttempts ||
				state.DLQAt != nil || state.Reason != "" || state.Stop == nil || state.Lease != nil || state.Next != nil {
				t.Fatalf("processing unnecessary damaged conditions: events %+v, source calls %d, state %+v", callback.events, source.calls, state)
			}
		})
	}
	t.Run("missing client recovers after technical retry", func(t *testing.T) {
		id := createDLQFixture(t, store, "temporarily-missing", "restore", true)
		if _, err := pool.Exec(t.Context(), "UPDATE notifier.t_notification SET failed_attempt_count = 4 WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
		claim := reserveDLQFixture(t, pool, id, time.Minute)
		before := readDLQState(t, pool, id)
		started := time.Now()
		callback := &dlqCallback{}
		worker.ProcessNotification(t.Context(), claim, store, nil, callback, settings)
		after := readDLQState(t, pool, id)
		if len(callback.events) != 0 || after.Next == nil || after.Next.Before(started.Add(settings.TechnicalRetryDelay)) || !after.Next.After(after.Deadline) {
			t.Fatalf("missing client retry: callbacks %+v, state %+v", callback.events, after)
		}
		want := before
		want.Next, want.Lease = after.Next, nil
		if !reflect.DeepEqual(after, want) {
			t.Fatalf("missing client changed lifecycle: %+v, want %+v", after, want)
		}
		if claims, err := store.ClaimNotifications(t.Context(), 1, time.Minute); err != nil || len(claims) != 0 {
			t.Fatalf("missing client immediately reclaimed = %+v, %v", claims, err)
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO notifier.t_client (client_id, callback_url, max_lifetime_sec)
			VALUES ('temporarily-missing', 'http://example.test/restored', 7200)`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), "UPDATE notifier.t_notification SET next_scheduled_dt = clock_timestamp() WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
		claims, err := store.ClaimNotifications(t.Context(), 1, time.Minute)
		if err != nil || len(claims) != 1 || claims[0].ID != id {
			t.Fatalf("claim restored client = %+v, %v", claims, err)
		}
		worker.ProcessNotification(t.Context(), claims[0], store, nil, callback, settings)
		state := readDLQState(t, pool, id)
		if len(callback.events) != 1 || callback.events[0].Status != core.EventDeadlineExceeded || state.Status != core.EventDeadlineExceeded ||
			state.Delivery != core.DeliveryDelivered || state.Attempts != 4 || state.DLQAt != nil || state.Reason != "" {
			t.Fatalf("restored client = events %+v, state %+v", callback.events, state)
		}
	})
}

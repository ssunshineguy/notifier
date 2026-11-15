//go:build integration

package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/ssunshineguy/notifier/notifier/db/migrations"
)

func TestDLQMigrationFreshAndBootstrapIntegration(t *testing.T) {
	for _, stagedBootstrap := range []bool{false, true} {
		name := "fresh"
		if stagedBootstrap {
			name = "bootstrap through version 2 then version 3"
		}
		t.Run(name, func(t *testing.T) {
			db := newMigrationDatabase(t)
			provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
			if err != nil {
				t.Fatal(err)
			}
			before := "[]"
			if stagedBootstrap {
				if _, err := provider.UpTo(t.Context(), 2); err != nil {
					t.Fatal(err)
				}
				assertVersion(t, provider, 2)
				assertFinalStatusSchema(t, db)
				execMigrationSQL(t, db, `INSERT INTO notifier.t_notification
                    (id,client_id,external_id,data,start_dt,deadline_dt,next_scheduled_dt,notification_status,delivery_status,stop_dt)
                    VALUES
                    ('00000000-0000-0000-0000-000000000001','fixture','active','{}',now(),now()+interval '1 day',now(),'active',NULL,NULL),
                    ('00000000-0000-0000-0000-000000000002','fixture','retry','[]',now(),now()+interval '1 day',now(),'success','trying_to_deliver',NULL),
                    ('00000000-0000-0000-0000-000000000003','fixture','stopped','[]',now(),now()+interval '1 day',NULL,'deadline_exceeded','delivered',now())`)
				before = migrationRowsSnapshot(t, db, "notifier.t_notification", "id")
			}
			if _, err := provider.Up(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertVersion(t, provider, 3)
			assertDLQSchema(t, db)
			if got := migrationRowsSnapshot(t, db, "notifier.t_notification", "id"); got != before {
				t.Fatal("migration changed the existing notification rows")
			}
			if got := migrationRowsSnapshot(t, db, "notifier.t_dlq", "notification_id"); got != "[]" {
				t.Fatal("migration unexpectedly populated DLQ")
			}
			execMigrationSQL(t, db, `INSERT INTO notifier.t_notification
                (id,client_id,external_id,data,start_dt,deadline_dt,next_scheduled_dt)
                VALUES ('00000000-0000-0000-0000-000000000005','fixture','dlq','{"damaged":"conditions"}',now(),now()+interval '1 day',now());
                INSERT INTO notifier.t_dlq (notification_id, notification, reason, created_dt)
                SELECT id, to_jsonb(notification), 'invalid_stored_conditions', clock_timestamp()
                FROM notifier.t_notification AS notification
                WHERE id='00000000-0000-0000-0000-000000000005';
                UPDATE notifier.t_notification AS notification
                SET notification_status='dlq', stop_dt=dlq.created_dt, next_scheduled_dt=NULL
                FROM notifier.t_dlq AS dlq WHERE notification.id=dlq.notification_id`)
			mainBeforeRerun := migrationRowsSnapshot(t, db, "notifier.t_notification", "id")
			dlqBeforeRerun := migrationRowsSnapshot(t, db, "notifier.t_dlq", "notification_id")
			if result, err := provider.Up(t.Context()); err != nil || len(result) != 0 {
				t.Fatalf("second Up=%d err=%v", len(result), err)
			}
			for _, name := range []string{"002__t_notification.sql", "003__notification_dlq.sql"} {
				raw, err := migrations.FS.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback() }()
				if _, err := tx.ExecContext(t.Context(), string(raw)); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			assertDLQSchema(t, db)
			if migrationRowsSnapshot(t, db, "notifier.t_notification", "id") != mainBeforeRerun ||
				migrationRowsSnapshot(t, db, "notifier.t_dlq", "notification_id") != dlqBeforeRerun {
				t.Fatal("rerunning DDL changed a notification or its DLQ snapshot")
			}
		})
	}
}

func assertDLQSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	assertFinalStatusSchema(t, db)
	var count int
	var columns int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns
        WHERE table_schema='notifier' AND table_name='t_notification' AND column_name IN ('dlq_dt','dlq_reason')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("unexpected main-row metadata columns=%d err=%v", columns, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns
        WHERE table_schema='notifier' AND table_name='t_dlq' AND is_nullable='NO' AND column_default IS NULL AND (
            (column_name='notification_id' AND udt_name='uuid') OR
            (column_name='notification' AND udt_name='jsonb') OR
            (column_name='reason' AND udt_name='text') OR
            (column_name='created_dt' AND udt_name='timestamptz'))`).Scan(&columns); err != nil || columns != 4 {
		t.Fatalf("DLQ table columns=%d err=%v", columns, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_constraint
        WHERE conrelid='notifier.t_dlq'::regclass AND convalidated AND (
            (conname='pk_dlq_notification_id' AND contype='p') OR
            (conname='fk_dlq_notification_id' AND contype='f'
                AND confrelid='notifier.t_notification'::regclass AND confdeltype='c') OR
            (conname IN ('ck_dlq_notification','ck_dlq_reason') AND contype='c'))`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("validated DLQ constraints=%d err=%v", count, err)
	}
}

func migrationRowsSnapshot(t *testing.T, db *sql.DB, table, orderColumn string) string {
	t.Helper()
	var snapshot string
	if err := db.QueryRowContext(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY "+orderColumn+"), '[]'::jsonb)::text FROM "+table+" AS item").Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

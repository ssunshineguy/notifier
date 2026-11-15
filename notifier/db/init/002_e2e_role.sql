BEGIN;

SET LOCAL lock_timeout = '6s';
SELECT pg_advisory_xact_lock(hashtextextended('notifier_e2e bootstrap', 0));

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'notifier_e2e') THEN
        CREATE ROLE notifier_e2e LOGIN;
    END IF;
END
$$;

ALTER ROLE notifier_e2e PASSWORD 'notifier_e2e';
ALTER ROLE notifier_e2e SET search_path = notifier;

GRANT CONNECT ON DATABASE notifier TO notifier_e2e;
GRANT USAGE ON SCHEMA notifier TO notifier_e2e;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA notifier TO notifier_e2e;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA notifier TO notifier_e2e;

ALTER DEFAULT PRIVILEGES FOR ROLE notifier_migrator IN SCHEMA notifier
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO notifier_e2e;
ALTER DEFAULT PRIVILEGES FOR ROLE notifier_migrator IN SCHEMA notifier
    GRANT USAGE, SELECT ON SEQUENCES TO notifier_e2e;

COMMIT;

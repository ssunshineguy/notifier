-- Local stand only: in production roles are created by the infrastructure.

CREATE ROLE notifier_migrator LOGIN PASSWORD 'notifier_migrator';
CREATE ROLE notifier_api LOGIN PASSWORD 'notifier_api';
CREATE ROLE notifier_worker LOGIN PASSWORD 'notifier_worker';
CREATE ROLE notifier_collector LOGIN PASSWORD 'notifier_collector';

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA notifier AUTHORIZATION notifier_migrator;

ALTER ROLE notifier_migrator SET search_path = notifier;
ALTER ROLE notifier_api SET search_path = notifier;
ALTER ROLE notifier_worker SET search_path = notifier;
ALTER ROLE notifier_collector SET search_path = notifier;

GRANT CONNECT ON DATABASE notifier
    TO notifier_migrator, notifier_api, notifier_worker, notifier_collector;
GRANT USAGE ON SCHEMA notifier TO notifier_api, notifier_worker, notifier_collector;

ALTER DEFAULT PRIVILEGES FOR ROLE notifier_migrator IN SCHEMA notifier
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES
    TO notifier_api, notifier_worker, notifier_collector;
ALTER DEFAULT PRIVILEGES FOR ROLE notifier_migrator IN SCHEMA notifier
    GRANT USAGE, SELECT ON SEQUENCES
    TO notifier_api, notifier_worker, notifier_collector;

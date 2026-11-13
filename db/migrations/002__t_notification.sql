-- +goose Up
CREATE TABLE IF NOT EXISTS notifier.t_notification (
    id                   uuid        NOT NULL,
    client_id            text        NOT NULL,
    external_id          text        NOT NULL,
    data                 jsonb       NOT NULL,
    deadline_dt          timestamptz NOT NULL,
    start_dt             timestamptz NOT NULL DEFAULT now(),
    next_scheduled_dt    timestamptz NULL,
    lock_until_dt        timestamptz NULL,
    failed_attempt_count smallint    NOT NULL DEFAULT 0,
    stop_dt              timestamptz NULL,
    stop_reason          text        NULL,

    CONSTRAINT pk_notification_id PRIMARY KEY (id),
    CONSTRAINT uq_notification_client_id_external_id UNIQUE (client_id, external_id),
    CONSTRAINT ck_notification_external_id CHECK (length(external_id) BETWEEN 1 AND 128),
    CONSTRAINT ck_notification_deadline_dt_start_dt CHECK (deadline_dt > start_dt),
    CONSTRAINT ck_notification_failed_attempt_count CHECK (failed_attempt_count >= 0),
    CONSTRAINT ck_notification_stop_dt_stop_reason CHECK ((stop_dt IS NULL) = (stop_reason IS NULL)),
    CONSTRAINT ck_notification_stop_reason
        CHECK (stop_reason IN ('delivered', 'deadline_exceeded', 'delivery_failed', 'deleted'))
);

CREATE INDEX IF NOT EXISTS ix_notification_next_scheduled_dt
    ON notifier.t_notification (next_scheduled_dt)
    WHERE stop_dt IS NULL AND next_scheduled_dt IS NOT NULL;

CREATE INDEX IF NOT EXISTS ix_notification_stop_dt
    ON notifier.t_notification (stop_dt)
    WHERE stop_dt IS NOT NULL;

COMMENT ON COLUMN notifier.t_notification.client_id IS
    'No foreign key to t_client: clients will move to the Directory service.';

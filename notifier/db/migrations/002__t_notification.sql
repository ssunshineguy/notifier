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
    notification_status  text        NOT NULL DEFAULT 'active',
    delivery_status      text        NULL,

    CONSTRAINT pk_notification_id PRIMARY KEY (id),
    CONSTRAINT uq_notification_client_id_external_id UNIQUE (client_id, external_id),
    CONSTRAINT ck_notification_external_id CHECK (length(external_id) BETWEEN 1 AND 128),
    CONSTRAINT ck_notification_deadline_dt_start_dt CHECK (deadline_dt > start_dt),
    CONSTRAINT ck_notification_failed_attempt_count CHECK (failed_attempt_count >= 0),
    CONSTRAINT ck_notification_notification_status
        CHECK (notification_status IN ('active', 'success', 'deadline_exceeded', 'dlq')),
    CONSTRAINT ck_notification_delivery_status
        CHECK (delivery_status IN ('trying_to_deliver', 'delivered', 'delivery_failed')),
    CONSTRAINT ck_notification_notification_status_delivery_status
        CHECK ((notification_status IN ('active', 'dlq')) = (delivery_status IS NULL)),
    CONSTRAINT ck_notification_stop_dt_delivery_status
        CHECK (delivery_status IS NULL OR ((stop_dt IS NULL) = (delivery_status = 'trying_to_deliver'))),
    CONSTRAINT ck_notification_notification_status_stop_dt
        CHECK (notification_status <> 'dlq' OR
            (stop_dt IS NOT NULL AND next_scheduled_dt IS NULL AND lock_until_dt IS NULL))
);

CREATE INDEX IF NOT EXISTS ix_notification_next_scheduled_dt
    ON notifier.t_notification (next_scheduled_dt)
    WHERE stop_dt IS NULL AND next_scheduled_dt IS NOT NULL;

CREATE INDEX IF NOT EXISTS ix_notification_stop_dt
    ON notifier.t_notification (stop_dt)
    WHERE stop_dt IS NOT NULL;

COMMENT ON COLUMN notifier.t_notification.client_id IS
    'No foreign key to t_client: clients will move to the Directory service.';

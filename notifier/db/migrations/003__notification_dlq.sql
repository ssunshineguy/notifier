-- +goose Up
CREATE TABLE IF NOT EXISTS notifier.t_dlq (
    notification_id uuid        NOT NULL,
    notification    jsonb       NOT NULL,
    reason          text        NOT NULL,
    created_dt      timestamptz NOT NULL,
    CONSTRAINT pk_dlq_notification_id PRIMARY KEY (notification_id),
    CONSTRAINT fk_dlq_notification_id FOREIGN KEY (notification_id)
        REFERENCES notifier.t_notification (id) ON DELETE CASCADE,
    CONSTRAINT ck_dlq_notification CHECK (jsonb_typeof(notification) = 'object'),
    CONSTRAINT ck_dlq_reason CHECK (reason = 'invalid_stored_conditions')
);

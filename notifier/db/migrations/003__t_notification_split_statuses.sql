-- +goose Up
SET LOCAL lock_timeout = '6s';
LOCK TABLE notifier.t_notification IN ACCESS EXCLUSIVE MODE;

ALTER TABLE notifier.t_notification
    ADD COLUMN IF NOT EXISTS notification_status text NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS delivery_status text;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'notifier' AND table_name = 't_notification' AND column_name = 'stop_reason'
    ) THEN
        UPDATE notifier.t_notification
        SET notification_status = CASE
                WHEN stop_reason = 'deadline_exceeded' THEN 'deadline_exceeded'
                WHEN stop_reason = 'delivery_failed' AND stop_dt >= deadline_dt THEN 'deadline_exceeded'
                ELSE 'success'
            END,
            delivery_status = CASE
                WHEN stop_reason = 'delivery_failed' THEN 'delivery_failed'
                ELSE 'delivered'
            END
        WHERE stop_dt IS NOT NULL AND stop_reason IN ('delivered', 'deadline_exceeded', 'delivery_failed');
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE notifier.t_notification
    DROP COLUMN IF EXISTS stop_reason,
    DROP CONSTRAINT IF EXISTS ck_notification_notification_status,
    DROP CONSTRAINT IF EXISTS ck_notification_delivery_status,
    DROP CONSTRAINT IF EXISTS ck_notification_notification_status_delivery_status,
    DROP CONSTRAINT IF EXISTS ck_notification_stop_dt_delivery_status;

ALTER TABLE notifier.t_notification
    ADD CONSTRAINT ck_notification_notification_status
        CHECK (notification_status IN ('active', 'success', 'deadline_exceeded')) NOT VALID,
    ADD CONSTRAINT ck_notification_delivery_status
        CHECK (delivery_status IN ('trying_to_deliver', 'delivered', 'delivery_failed')) NOT VALID,
    ADD CONSTRAINT ck_notification_notification_status_delivery_status
        CHECK ((notification_status = 'active') = (delivery_status IS NULL)) NOT VALID,
    ADD CONSTRAINT ck_notification_stop_dt_delivery_status
        CHECK (delivery_status IS NULL OR ((stop_dt IS NULL) = (delivery_status = 'trying_to_deliver'))) NOT VALID;

ALTER TABLE notifier.t_notification
    VALIDATE CONSTRAINT ck_notification_notification_status,
    VALIDATE CONSTRAINT ck_notification_delivery_status,
    VALIDATE CONSTRAINT ck_notification_notification_status_delivery_status,
    VALIDATE CONSTRAINT ck_notification_stop_dt_delivery_status;

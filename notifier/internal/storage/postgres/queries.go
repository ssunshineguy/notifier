package postgres

const checkConnection = "SELECT 1"

const selectClient = `
SELECT client_id, callback_url, max_lifetime_sec, callback_timeout_ms, created_dt
FROM notifier.t_client
WHERE client_id = $1`

const (
	insertNotification = `
INSERT INTO notifier.t_notification
    (id, client_id, external_id, data, deadline_dt, start_dt, next_scheduled_dt)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT ON CONSTRAINT uq_notification_client_id_external_id DO NOTHING
RETURNING id`

	selectNotificationID = `
SELECT id FROM notifier.t_notification
WHERE client_id = $1 AND external_id = $2`

	selectNotification = `
SELECT notification.id, notification.external_id, notification.data,
       notification.deadline_dt, notification.start_dt, notification.notification_status,
       COALESCE(notification.delivery_status, ''), notification.stop_dt, dlq.created_dt, COALESCE(dlq.reason, '')
FROM notifier.t_notification AS notification
LEFT JOIN notifier.t_dlq AS dlq ON dlq.notification_id = notification.id
WHERE notification.id = $1 AND notification.client_id = $2`

	updateNotification = `
WITH owned AS (
    SELECT id, external_id, deadline_dt, start_dt, notification_status, delivery_status, stop_dt, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND client_id = $2
    FOR UPDATE
), updated AS (
    UPDATE notifier.t_notification AS notification
    SET data = $3
    FROM owned
    WHERE notification.id = owned.id AND owned.stop_dt IS NULL
      AND (owned.lock_until_dt IS NULL OR owned.lock_until_dt <= clock_timestamp())
    RETURNING notification.id
)
SELECT owned.id, owned.external_id, owned.deadline_dt, owned.start_dt,
       owned.notification_status, COALESCE(owned.delivery_status, ''),
       owned.stop_dt IS NOT NULL, updated.id IS NOT NULL
FROM owned LEFT JOIN updated USING (id)`

	deleteNotification = `
WITH owned AS (
    SELECT id, stop_dt, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND client_id = $2
    FOR UPDATE
), deleted AS (
    DELETE FROM notifier.t_notification AS notification
    USING owned
    WHERE notification.id = owned.id
      AND (owned.stop_dt IS NOT NULL OR owned.lock_until_dt IS NULL OR owned.lock_until_dt <= clock_timestamp())
    RETURNING notification.id
)
SELECT owned.id, deleted.id IS NOT NULL
FROM owned LEFT JOIN deleted USING (id)`
)

const (
	claimNotifications = `
WITH due AS (
    SELECT id
    FROM notifier.t_notification
    WHERE stop_dt IS NULL AND next_scheduled_dt <= now()
      AND (lock_until_dt IS NULL OR lock_until_dt <= now())
    ORDER BY next_scheduled_dt, id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE notifier.t_notification AS notification
SET lock_until_dt = statement_timestamp() + $2::bigint * interval '1 millisecond'
FROM due
WHERE notification.id = due.id
RETURNING notification.id, notification.lock_until_dt`

	selectClaimedNotificationConditions = `
SELECT client_id, external_id, data, deadline_dt, notification_status, failed_attempt_count
FROM notifier.t_notification
WHERE id = $1 AND stop_dt IS NULL
  AND lock_until_dt = $2 AND lock_until_dt > now()`

	selectClaimedCallbackTarget = `
WITH reserved AS (
    SELECT client_id, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND stop_dt IS NULL AND lock_until_dt = $2
    FOR UPDATE
)
SELECT client.callback_url, client.callback_timeout_ms
FROM reserved
LEFT JOIN notifier.t_client AS client ON client.client_id = reserved.client_id
WHERE reserved.lock_until_dt > clock_timestamp()`

	updateNotificationResult = `
WITH reserved AS (
    SELECT id, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND stop_dt IS NULL AND lock_until_dt = $2
    FOR UPDATE
)
UPDATE notifier.t_notification AS notification
SET next_scheduled_dt = $3,
    failed_attempt_count = $4,
    notification_status = $5,
    delivery_status = NULLIF($6::text, ''),
    stop_dt = CASE WHEN $6::text IN ('delivered', 'delivery_failed') THEN clock_timestamp() END,
    lock_until_dt = NULL
FROM reserved
WHERE notification.id = reserved.id AND reserved.lock_until_dt > clock_timestamp()
RETURNING notification.id`

	rescheduleNotification = `
WITH reserved AS (
    SELECT id, lock_until_dt
    FROM notifier.t_notification
    WHERE id = $1 AND stop_dt IS NULL AND lock_until_dt = $2
    FOR UPDATE
)
UPDATE notifier.t_notification AS notification
SET next_scheduled_dt = $3, lock_until_dt = NULL
FROM reserved
WHERE notification.id = reserved.id AND reserved.lock_until_dt > clock_timestamp()
RETURNING notification.id`

	moveNotificationToDLQ = `
WITH reserved AS (
    SELECT notification.*
    FROM notifier.t_notification AS notification
    WHERE id = $1 AND stop_dt IS NULL AND notification_status = 'active' AND lock_until_dt = $2
    FOR UPDATE
), transition AS (
    SELECT id, to_jsonb(reserved) AS original, clock_timestamp() AS stopped_at
    FROM reserved
    WHERE lock_until_dt > clock_timestamp()
), stopped AS (
    UPDATE notifier.t_notification AS notification
    SET notification_status = 'dlq', delivery_status = NULL,
        stop_dt = transition.stopped_at, next_scheduled_dt = NULL, lock_until_dt = NULL
    FROM transition
    WHERE notification.id = transition.id
    RETURNING notification.id
)
INSERT INTO notifier.t_dlq (notification_id, notification, reason, created_dt)
SELECT stopped.id, transition.original, $3, transition.stopped_at
FROM stopped JOIN transition USING (id)
RETURNING notification_id`
)

const deleteStoppedNotifications = `
WITH expired AS (
    SELECT id
    FROM notifier.t_notification
    WHERE stop_dt < $1
      AND notification_status IN ('success', 'deadline_exceeded')
    ORDER BY stop_dt, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
), deleted AS (
    DELETE FROM notifier.t_notification AS notification
    USING expired
    WHERE notification.id = expired.id
    RETURNING notification.id
)
SELECT count(*) FROM deleted`

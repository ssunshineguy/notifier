-- +goose Up
CREATE TABLE IF NOT EXISTS notifier.t_client (
    client_id           text        NOT NULL,
    callback_url        text        NOT NULL,
    max_lifetime_sec    integer     NULL,
    callback_timeout_ms integer     NULL,
    created_dt          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT pk_client_client_id PRIMARY KEY (client_id),
    CONSTRAINT ck_client_client_id CHECK (length(client_id) BETWEEN 1 AND 64),
    CONSTRAINT ck_client_callback_url CHECK (length(callback_url) BETWEEN 1 AND 128),
    CONSTRAINT ck_client_max_lifetime_sec CHECK (max_lifetime_sec > 0),
    CONSTRAINT ck_client_callback_timeout_ms CHECK (callback_timeout_ms > 0)
);

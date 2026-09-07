-- Transactional outbox (ADR 0020). One row per already-encoded Kafka
-- message (event x topic): the use case's transaction inserts here instead
-- of writing to the broker, and the in-process relay drains rows onto
-- Kafka in id order.
CREATE TABLE outbox_events (
    id           BIGSERIAL PRIMARY KEY,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    key          BYTEA,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT
);

CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

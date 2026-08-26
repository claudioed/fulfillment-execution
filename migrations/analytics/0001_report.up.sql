-- Fulfillment throughput analytics read model (ADR-0012).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/fulfillment-projector and read (read-only) by
-- cmd/fulfillment-reports. The tables here are projections derived from the
-- analytics event stream, not sources of truth.

-- Idempotency + freshness: every applied analytics event id is recorded
-- here exactly once. applied_at is wall-clock insert time; occurred_at is
-- the event's business time, used to compute the projection's freshness lag.
CREATE TABLE analytics_processed_events (
    event_id    TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_analytics_processed_events_occurred_at
    ON analytics_processed_events (occurred_at DESC);

-- Consumer-level dedupe set, used by the inbound consumer's
-- ports.ProcessedEvents gate. It is kept SEPARATE from
-- analytics_processed_events (which the projection UPSERT claims) so the two
-- idempotency layers do not race to claim the same event_id: the consumer
-- gate admits the event, the projection then records its effect.
CREATE TABLE analytics_consumed_events (
    event_id     TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pending claims: the claim time of a task not yet completed, so a later
-- TaskCompleted can derive claim-to-complete seconds. Keyed by the natural
-- identity of a claim (task_type, station_id, task_id).
CREATE TABLE analytics_pending_claims (
    task_type  TEXT NOT NULL,
    station_id TEXT NOT NULL,
    task_id    TEXT NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (task_type, station_id, task_id)
);

-- The throughput rollup fact table: one row per (task_type, station_id,
-- hour_bucket). Counters and the running claim-to-complete sum are UPSERTed
-- as events arrive; the average is derived at read time from the two
-- claim_to_complete columns.
CREATE TABLE throughput_rollup (
    task_type                  TEXT NOT NULL,
    station_id                 TEXT NOT NULL,
    hour_bucket                TIMESTAMPTZ NOT NULL,
    completions                BIGINT NOT NULL DEFAULT 0,
    lease_expiries             BIGINT NOT NULL DEFAULT 0,
    weigh_check_diverts        BIGINT NOT NULL DEFAULT 0,
    claim_to_complete_seconds  DOUBLE PRECISION NOT NULL DEFAULT 0,
    completions_with_claim     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (task_type, station_id, hour_bucket)
);

CREATE INDEX idx_throughput_rollup_hour_bucket
    ON throughput_rollup (hour_bucket);

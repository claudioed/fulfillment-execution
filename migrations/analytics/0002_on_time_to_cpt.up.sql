-- On-time-to-CPT KPI columns on the throughput rollup (ADR-0026, companion
-- to order-management ADR 0014 §6). Additive only: no existing column is
-- touched, and existing rows default every new column to 0 so the shape of
-- every previously-projected row is unchanged.
--
-- Grain: the SAME (task_type, station_id, hour_bucket) key the rollup
-- already uses. A manifested package has no task-type/station identity of
-- its own; it inherits its originating SLAM task's dimensions (resolved by
-- the publisher via a TaskRepo lookup — see
-- internal/adapters/outbound/kafka/analytics_publisher.go), so task_type is
-- always "SLAM" for these counters in practice. A dedicated dimension was
-- considered and rejected: SLAM rows already exist meaningfully in this
-- rollup via ApplyTaskCompleted, so reusing the grain avoids a second,
-- parallel key space for what is really the same process path.
ALTER TABLE throughput_rollup
    ADD COLUMN packages_manifested    BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN packages_on_time_cpt   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN packages_late_cpt      BIGINT NOT NULL DEFAULT 0;

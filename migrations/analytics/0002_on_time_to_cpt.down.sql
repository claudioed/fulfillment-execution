ALTER TABLE throughput_rollup
    DROP COLUMN IF EXISTS packages_manifested,
    DROP COLUMN IF EXISTS packages_on_time_cpt,
    DROP COLUMN IF EXISTS packages_late_cpt;

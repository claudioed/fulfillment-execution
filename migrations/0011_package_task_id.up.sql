-- Adds the owning Pack task's id to packages, so SealPackage can dedupe a
-- retried POST /tasks/{id}/seal-package against an already-sealed Package
-- instead of creating a duplicate (see REST_AUDIT.md's flagged idempotency
-- gap and internal/application/usecases/seal_package.go).
--
-- Nullable and unindexed-unique deliberately: existing rows have no task_id
-- (backfilling one would require guessing, which this migration does not
-- attempt), and a partial unique index (only enforced when task_id IS NOT
-- NULL) preserves that history while still giving every package sealed
-- from this point on a hard, race-safe uniqueness guarantee — the
-- ON CONFLICT DO NOTHING in package_repo.go's Save relies on this index
-- existing, not merely on the use case's own FindByTaskId pre-check.
ALTER TABLE packages ADD COLUMN task_id TEXT;
CREATE UNIQUE INDEX idx_packages_task_id ON packages (task_id) WHERE task_id IS NOT NULL;

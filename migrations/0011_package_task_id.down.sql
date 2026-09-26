DROP INDEX IF EXISTS idx_packages_task_id;
ALTER TABLE packages DROP COLUMN task_id;

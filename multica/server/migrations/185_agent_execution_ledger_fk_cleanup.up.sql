-- Defensive no-op on a fresh 184 install. This also repairs development/test
-- databases that applied an early 184 draft before the old queue FK removal
-- was added to that migration.
ALTER TABLE agent_usage DROP CONSTRAINT IF EXISTS task_usage_task_id_fkey;

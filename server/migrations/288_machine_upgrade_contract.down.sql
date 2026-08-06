BEGIN;

-- Rollback note: only roll this back before any Machine Upgrade is accepted.
-- The down migration intentionally removes queued/terminal audit history.
DROP TABLE IF EXISTS machine_upgrade;

COMMIT;

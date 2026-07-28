DROP INDEX IF EXISTS sandbox_snapshot_checkpoint_idx;

ALTER TABLE sandbox_snapshot
    DROP COLUMN IF EXISTS checkpoint_id;

ALTER TABLE env_checkpoint
    DROP CONSTRAINT IF EXISTS env_checkpoint_save_mode_check;

ALTER TABLE env_checkpoint
    DROP COLUMN IF EXISTS save_mode;

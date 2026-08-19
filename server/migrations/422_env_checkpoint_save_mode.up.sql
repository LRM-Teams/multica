-- save_mode distinguishes the two first-class checkpoint modes.
-- pause_in_place (the default, and what every pre-existing row resolves to)
-- suspends the source instances and records no savepoint, so the checkpoint can
-- be materialized exactly once. snapshot records an immutable savepoint per
-- source instance and leaves the source running, so the checkpoint can be
-- materialized into any number of independent lanes.
ALTER TABLE env_checkpoint
    ADD COLUMN IF NOT EXISTS save_mode text NOT NULL DEFAULT 'pause_in_place';

ALTER TABLE env_checkpoint
    DROP CONSTRAINT IF EXISTS env_checkpoint_save_mode_check,
    ADD CONSTRAINT env_checkpoint_save_mode_check
        CHECK (save_mode IN ('pause_in_place', 'snapshot'));

-- A savepoint is owned by exactly one checkpoint (no reference counting).
-- Deleting the checkpoint cascades the ownership row away; the Cube template
-- itself is released by a delete_template job, which must read cube_snapshot_id
-- before this row disappears. NULL means a snapshot nobody owns, which is every
-- pre-existing row and every user-created snapshot.
ALTER TABLE sandbox_snapshot
    ADD COLUMN IF NOT EXISTS checkpoint_id uuid
        REFERENCES env_checkpoint(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS sandbox_snapshot_checkpoint_idx
    ON sandbox_snapshot (checkpoint_id)
    WHERE checkpoint_id IS NOT NULL;

BEGIN;

-- A local Active-pointer restore is not rollback success. Preserve the exact
-- incumbent version and a distinct restored generation so the server can wait
-- for the original full managed set to prove it returned.
ALTER TABLE machine_upgrade
    ADD COLUMN source_version TEXT,
    ADD COLUMN rollback_generation TEXT,
    ADD COLUMN rollback_runtime_ids UUID[] NOT NULL DEFAULT '{}';

ALTER TABLE machine_upgrade DROP CONSTRAINT machine_upgrade_phase_check;
ALTER TABLE machine_upgrade ADD CONSTRAINT machine_upgrade_phase_check
    CHECK (phase IN (
        'queued', 'starting', 'staging', 'verifying', 'handoff',
        'converging', 'rollback_pending', 'completed', 'failed',
        'rolled_back', 'timeout', 'cancelled'
    ));

ALTER TABLE machine_upgrade DROP CONSTRAINT machine_upgrade_check;
ALTER TABLE machine_upgrade ADD CONSTRAINT machine_upgrade_check CHECK (
    (phase IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled'))
    = (result IS NOT NULL)
);

COMMIT;

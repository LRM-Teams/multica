BEGIN;

ALTER TABLE machine_upgrade DROP CONSTRAINT machine_upgrade_check;
ALTER TABLE machine_upgrade ADD CONSTRAINT machine_upgrade_check CHECK (
    (phase IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled'))
    = (result IS NOT NULL)
);
ALTER TABLE machine_upgrade DROP CONSTRAINT machine_upgrade_phase_check;
ALTER TABLE machine_upgrade ADD CONSTRAINT machine_upgrade_phase_check
    CHECK (phase IN (
        'queued', 'starting', 'staging', 'verifying', 'handoff',
        'converging', 'completed', 'failed', 'rolled_back', 'timeout',
        'cancelled'
    ));
ALTER TABLE machine_upgrade
    DROP COLUMN rollback_runtime_ids,
    DROP COLUMN rollback_generation,
    DROP COLUMN source_version;

COMMIT;

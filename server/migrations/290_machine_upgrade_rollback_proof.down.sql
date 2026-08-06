BEGIN;

-- The prior schema has no rollback_pending state. Do not claim success merely
-- because the Active pointer was restored: make an in-flight rollback a
-- terminal failure before narrowing the phase CHECK constraint.
UPDATE machine_upgrade
SET phase = 'failed',
    result = 'failed',
    completed_at = COALESCE(completed_at, now()),
    error_code = COALESCE(error_code, 'rollback_proof_unavailable_after_downgrade'),
    error_message = COALESCE(error_message, 'rollback proof could not be retained after schema downgrade'),
    updated_at = now()
WHERE phase = 'rollback_pending';

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

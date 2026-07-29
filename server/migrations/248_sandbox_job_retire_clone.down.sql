-- Restores 'clone' as an accepted job type, matching migration 187. Rolling back
-- the schema does not restore the code that produced or handled those jobs, so a
-- rollback only reopens the constraint for an older server image.
ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure', 'clone',
        'create_template', 'delete_template', 'exec', 'message'
    ));

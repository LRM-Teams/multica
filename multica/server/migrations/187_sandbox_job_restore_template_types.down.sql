ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure', 'clone', 'exec', 'message'
    ));

ALTER TABLE sandbox_instance
    DROP CONSTRAINT IF EXISTS sandbox_instance_status_check,
    ADD CONSTRAINT sandbox_instance_status_check CHECK (status IN (
        'pending', 'creating', 'running', 'failed', 'stopping', 'stopped',
        'resuming', 'reconfiguring', 'snapshotting'
    ));

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure', 'create_template', 'exec', 'message'
    ));

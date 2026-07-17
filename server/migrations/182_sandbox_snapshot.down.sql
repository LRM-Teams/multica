DELETE FROM sandbox_job WHERE type = 'delete_template';
DELETE FROM sandbox_job WHERE instance_id IS NULL;

ALTER TABLE sandbox_job
    ALTER COLUMN instance_id SET NOT NULL;

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure',
        'create_template', 'exec', 'message'
    ));

DROP TABLE IF EXISTS sandbox_snapshot;

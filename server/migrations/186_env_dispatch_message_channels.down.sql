DELETE FROM sandbox_job WHERE type = 'clone';

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure',
        'create_template', 'delete_template', 'exec', 'message'
    ));

DROP TABLE IF EXISTS environment_agent_sandbox;

ALTER TABLE environment
    DROP COLUMN IF EXISTS collaboration_trigger;

-- Migration 186 rewrote sandbox_job_type_check when adding 'clone' but dropped
-- create_template / delete_template that 181/182 had already introduced.
ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure', 'clone',
        'create_template', 'delete_template', 'exec', 'message'
    ));

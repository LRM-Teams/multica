-- Retires the 'clone' job type. Branch provisioning was its only producer, and
-- it now creates sandboxes from a checkpoint-owned savepoint instead: clone took
-- a throwaway snapshot of a running source per destination, so re-expanding an
-- env paid for the same capture once per rollout and nothing kept the captured
-- state around.
--
-- Release order matters (design D7/D12): the server stops enqueueing 'clone'
-- first, then this migration drops the CHECK value, then sandboxd drops the
-- handler. Applying this before the server is deployed would fail any in-flight
-- branch dispatch on insert.
ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure',
        'create_template', 'delete_template', 'exec', 'message'
    ));

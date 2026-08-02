UPDATE sandbox_instance SET status = 'running' WHERE status = 'reconfiguring';

ALTER TABLE sandbox_instance
    DROP CONSTRAINT IF EXISTS sandbox_instance_status_check,
    ADD CONSTRAINT sandbox_instance_status_check CHECK (status IN ('pending', 'creating', 'running', 'failed', 'stopping', 'stopped', 'resuming'));

-- Task #101 (2026-08-02): the sibling sandbox_instance.status narrowing
-- above correctly remaps before narrowing; this one used to silently
-- DELETE any 'reconfigure' sandbox_job row instead — same commit
-- (99d7655c4), no discussion of why the two got different treatment. That
-- job type is real, active production data (sandbox.go / env_sandbox_lifecycle.go
-- both branch on it) with no safe remap target ('reconfigure' isn't
-- equivalent to any other job type this constraint still allows). Fail
-- loud instead, only when there's real data at stake — matching migration
-- 107/186's fix (task #99/#101).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM sandbox_job WHERE type = 'reconfigure';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 143 down cannot proceed: % row(s) in sandbox_job have type=''reconfigure''. There is no safe value to remap them to under the narrower type list this migration is reverting to. If you accept permanently losing these job records, run: DELETE FROM sandbox_job WHERE type = ''reconfigure''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN ('create', 'stop', 'resume', 'delete', 'exec', 'message'));

-- Task #101 (2026-08-02), two independent fixes to this file:
--
-- 1. This migration (originally numbered 183, renumbered to 186 on merge)
--    was authored before 181/182 landed 'create_template'/'delete_template'.
--    Its up.sql originally only added 'clone'; commit e0587aa9c later fixed
--    up.sql to also re-include 'create_template'/'delete_template' (which
--    186's DROP+ADD would otherwise have silently erased), but this down.sql
--    was never updated to match. It still narrows past those two values,
--    which this migration never added and has no business touching on
--    rollback. Restored below — this is a plain correctness fix, not a
--    judgment call.
--
-- 2. 'clone' IS a value this migration introduces, and sandbox_job rows
--    with that type are real, active production data (env_sandbox_lifecycle.go
--    enqueues them; sandbox.go branches on them). The original DELETE
--    silently destroyed them on rollback with no warning. Unlike
--    create_template/delete_template above, there's no question of "this
--    migration didn't add it" here — but silently deleting live job records
--    is the same failure shape task #99 fixed for migration 107: fail loud
--    instead, only when there's real data at stake.
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM sandbox_job WHERE type = 'clone';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 186 down cannot proceed: % row(s) in sandbox_job have type=''clone'' (the sandbox-clone feature this migration introduces). There is no safe value to remap them to under the narrower type list this migration is reverting to. If you accept permanently losing these job records, run: DELETE FROM sandbox_job WHERE type = ''clone''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure',
        'create_template', 'delete_template', 'exec', 'message'
    ));

DROP TABLE IF EXISTS environment_agent_sandbox;

ALTER TABLE environment
    DROP COLUMN IF EXISTS collaboration_trigger;

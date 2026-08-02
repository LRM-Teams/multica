UPDATE sandbox_instance SET status = 'running' WHERE status = 'snapshotting';

-- Task #101 (2026-08-02): 'create_template' sandbox_job rows are real,
-- active production data (sandbox.go's create-template flow, always with a
-- non-null instance_id) with no safe remap target under the narrower type
-- list this migration reverts to. The original DELETE silently destroyed
-- them on rollback with no warning — fail loud instead, only when there's
-- real data at stake (matching migration 107/143/186's fix, task #99/#101).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM sandbox_job WHERE type = 'create_template';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 181 down cannot proceed: % row(s) in sandbox_job have type=''create_template''. There is no safe value to remap them to under the narrower type list this migration is reverting to. If you accept permanently losing these job records, run: DELETE FROM sandbox_job WHERE type = ''create_template''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE sandbox_instance
    DROP CONSTRAINT IF EXISTS sandbox_instance_status_check,
    ADD CONSTRAINT sandbox_instance_status_check CHECK (status IN (
        'pending', 'creating', 'running', 'failed', 'stopping', 'stopped',
        'resuming', 'reconfiguring'
    ));

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure', 'exec', 'message'
    ));

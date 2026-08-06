-- Task #101 (2026-08-02): this down.sql had two independent silent-data-loss
-- DELETEs, both replaced with the same fail-loud-only-if-real-data pattern
-- as migration 107/143/181/186 (task #99/#101):
--
-- 1. 'delete_template' sandbox_job rows are real, active production data
--    (sandbox.go's snapshot-delete flow) with no safe remap target under
--    the narrower type list this migration reverts to.
-- 2. Separately, this migration's up.sql made instance_id nullable
--    (delete_template jobs can legitimately have a null instance_id — "may
--    be null if source instance was deleted", sandbox.go's own comment).
--    The down.sql's ALTER COLUMN ... SET NOT NULL needs the same guard: any
--    row with a null instance_id — of ANY type, not just delete_template,
--    in case a future job type also ends up with one — must block the
--    rollback instead of being silently deleted to make the column
--    constraint pass.
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM sandbox_job WHERE type = 'delete_template';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 182 down cannot proceed: % row(s) in sandbox_job have type=''delete_template''. There is no safe value to remap them to under the narrower type list this migration is reverting to. If you accept permanently losing these job records, run: DELETE FROM sandbox_job WHERE type = ''delete_template''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM sandbox_job WHERE instance_id IS NULL;
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 182 down cannot proceed: % row(s) in sandbox_job have a null instance_id, which this migration''s up.sql allowed (the down.sql is re-tightening instance_id to NOT NULL). There is no value to backfill instance_id with. If you accept permanently losing these job records, run: DELETE FROM sandbox_job WHERE instance_id IS NULL; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE sandbox_job
    ALTER COLUMN instance_id SET NOT NULL;

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure',
        'create_template', 'exec', 'message'
    ));

DROP TABLE IF EXISTS sandbox_snapshot;

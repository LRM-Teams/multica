-- 126_issue_fork_provenance.down.sql
DROP INDEX IF EXISTS idx_issue_forked_from;
ALTER TABLE issue
    DROP COLUMN IF EXISTS forked_at_task_id,
    DROP COLUMN IF EXISTS forked_at_seq,
    DROP COLUMN IF EXISTS forked_from_issue_id;

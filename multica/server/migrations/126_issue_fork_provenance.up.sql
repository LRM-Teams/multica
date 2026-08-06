-- 126_issue_fork_provenance.up.sql
-- Tracks issue fork provenance for multi-agent DAG RL training.
-- A row with forked_from_issue_id IS NULL is an original; non-NULL means it
-- was forked from another issue at (forked_at_task_id, forked_at_seq) -- the
-- branch point in the source agent's transcript (task_message.seq).

ALTER TABLE issue
    ADD COLUMN forked_from_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    ADD COLUMN forked_at_seq INTEGER,
    ADD COLUMN forked_at_task_id UUID;

-- Partial index: only forked issues. The common lookups are "find the original
-- for this fork" and "list all forks of this original". Original issues have a
-- NULL forked_from_issue_id and don't belong in this index.
CREATE INDEX idx_issue_forked_from
    ON issue (forked_from_issue_id)
    WHERE forked_from_issue_id IS NOT NULL;

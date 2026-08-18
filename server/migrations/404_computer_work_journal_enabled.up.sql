-- 404: Project Computer-local Machine Work Journal enablement onto the
-- owner row so list APIs can show it. The Computer file remains authoritative;
-- this column is updated only after the resident confirms a toggle.

ALTER TABLE computer_identity_owner
    ADD COLUMN IF NOT EXISTS work_journal_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Snapshot of the finished 写汇报 body so later bubble Q&A can read the
-- artifact even when the inserted page was deleted.

ALTER TABLE note_period_brief_run
    ADD COLUMN IF NOT EXISTS result_markdown TEXT;

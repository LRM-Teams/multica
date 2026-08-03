DROP INDEX IF EXISTS research_report_author_idx;

ALTER TABLE research_report_claim
  DROP COLUMN IF EXISTS anchor_quote;

ALTER TABLE research_report
  DROP COLUMN IF EXISTS author_agent_id,
  DROP COLUMN IF EXISTS produced_by_attempt_id,
  DROP COLUMN IF EXISTS produced_by_task_id;

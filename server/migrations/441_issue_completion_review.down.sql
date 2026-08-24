-- Roll back migration 441.
DROP TABLE IF EXISTS issue_completion_report;

-- Orphaned canonical history may have been retained after an Issue deletion
-- while this migration was active. Convert only those rows back to legacy
-- history before restoring the stricter pre-440 contract.
UPDATE agent_inbox_event
SET issue_run_kind = NULL,
    issue_execution_revision = NULL,
    issue_execution_attempt_number = NULL
WHERE issue_id IS NULL
  AND issue_run_kind = 'canonical';

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT agent_inbox_event_issue_run_contract_check,
  ADD CONSTRAINT agent_inbox_event_issue_run_contract_check CHECK (
    (
      issue_run_kind IS NULL
      AND issue_execution_revision IS NULL
      AND issue_execution_attempt_number IS NULL
    )
    OR (
      issue_run_kind = 'canonical'
      AND issue_id IS NOT NULL
      AND reason = 'issue'
      AND issue_execution_revision >= 0
      AND issue_execution_attempt_number > 0
    )
  );

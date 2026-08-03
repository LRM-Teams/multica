DROP INDEX IF EXISTS research_source_snapshot_projection_idx;
ALTER TABLE research_source DROP COLUMN IF EXISTS source_snapshot_id;

DROP INDEX IF EXISTS agent_inbox_event_research_dispatch_key_idx;
DROP INDEX IF EXISTS research_message_run_event_idx;
ALTER TABLE research_message DROP COLUMN IF EXISTS run_event_id;
DROP INDEX IF EXISTS research_graph_node_run_event_idx;
ALTER TABLE research_graph_node DROP COLUMN IF EXISTS run_event_id;

DROP TABLE IF EXISTS research_run_event;
DROP TABLE IF EXISTS research_report_claim;
DROP TABLE IF EXISTS research_decision;
DROP TABLE IF EXISTS research_claim_evidence;

DROP INDEX IF EXISTS research_report_run_version_idx;
ALTER TABLE research_report
  DROP COLUMN IF EXISTS goal_version,
  DROP COLUMN IF EXISTS plan_version;

ALTER TABLE research_question
  DROP CONSTRAINT IF EXISTS research_question_answer_claim_fk;
DROP TABLE IF EXISTS research_claim;
DROP TABLE IF EXISTS research_observation;
DROP TABLE IF EXISTS research_source_snapshot;
DROP TABLE IF EXISTS research_task_attempt;
DROP TABLE IF EXISTS research_task_dependency;

ALTER TABLE research_question
  DROP CONSTRAINT IF EXISTS research_question_created_by_task_fk;
DROP TABLE IF EXISTS research_task;
DROP TABLE IF EXISTS research_question;
DROP TABLE IF EXISTS research_contract_revision;

UPDATE research_session
SET status = 'archived', updated_at = now()
WHERE status IN ('failed', 'cancelled');

ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_status_check;
ALTER TABLE research_session
  ADD CONSTRAINT research_session_status_check
  CHECK (status IN (
    'drafting',
    'running',
    'awaiting_user_confirm',
    'completed',
    'archived',
    'paused'
  ));

DROP INDEX IF EXISTS research_session_reconcile_due_idx;
DROP INDEX IF EXISTS research_session_metrics_idx;
ALTER TABLE research_session
  DROP COLUMN IF EXISTS goal_version,
  DROP COLUMN IF EXISTS plan_version,
  DROP COLUMN IF EXISTS state_version,
  DROP COLUMN IF EXISTS orchestrator_version,
  DROP COLUMN IF EXISTS run_config,
  DROP COLUMN IF EXISTS run_stats,
  DROP COLUMN IF EXISTS run_initialized_at,
  DROP COLUMN IF EXISTS last_progress_at,
  DROP COLUMN IF EXISTS next_reconcile_at,
  DROP COLUMN IF EXISTS reconcile_lease_token,
  DROP COLUMN IF EXISTS reconcile_lease_expires_at,
  DROP COLUMN IF EXISTS stop_reason,
  DROP COLUMN IF EXISTS last_error;

DROP TABLE IF EXISTS issue_dispatch_outbox;
DROP TABLE IF EXISTS active_issue_execution;

DROP INDEX IF EXISTS agent_inbox_event_one_active_canonical_issue_run_idx;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_issue_run_contract_check,
  DROP COLUMN IF EXISTS issue_execution_attempt_number,
  DROP COLUMN IF EXISTS issue_execution_revision,
  DROP COLUMN IF EXISTS issue_run_kind;

DROP INDEX IF EXISTS issue_channel_goal_status_idx;

DROP TRIGGER IF EXISTS channel_goal_detach_issue_scope ON channel_goal;
DROP FUNCTION IF EXISTS detach_channel_goal_issue_scope();

ALTER TABLE issue
  DROP CONSTRAINT IF EXISTS issue_channel_goal_workspace_fkey,
  DROP CONSTRAINT IF EXISTS issue_goal_scope_consistent,
  DROP CONSTRAINT IF EXISTS issue_workspace_id_id_key,
  DROP COLUMN IF EXISTS execution_attempt_sequence,
  DROP COLUMN IF EXISTS execution_revision,
  DROP COLUMN IF EXISTS goal_required,
  DROP COLUMN IF EXISTS channel_goal_id;

ALTER TABLE channel_goal
  DROP CONSTRAINT IF EXISTS channel_goal_workspace_id_id_key,
  DROP COLUMN IF EXISTS execution_graph_revision;

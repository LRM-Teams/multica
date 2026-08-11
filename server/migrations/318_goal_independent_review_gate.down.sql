DROP TABLE IF EXISTS work_review_agent_assignment;

DROP INDEX IF EXISTS work_verification_reviewer_agent_idx;
DROP INDEX IF EXISTS work_verification_producer_active_idx;

ALTER TABLE work_verification_attempt
  DROP COLUMN IF EXISTS context_policy,
  DROP COLUMN IF EXISTS reviewer_agent_id,
  DROP COLUMN IF EXISTS producer_node_id;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

-- Remap rows before restoring the narrower pre-318 constraint. Goal Graph
-- deltas are directed coordination work, so `issue` is the closest retained
-- non-residual dispatch reason during rollback.
UPDATE agent_inbox_event
SET reason = 'issue'
WHERE reason = 'goal_graph_delta';

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','environment_dispatch','memory_curation',
    'reminder','channel_role_changed'
  ));

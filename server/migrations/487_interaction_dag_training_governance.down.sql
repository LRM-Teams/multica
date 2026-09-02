-- Down for 472: drop the training governance tables. The grant backfill and
-- the legacy session closure are NOT reversible (reviving closed sessions or
-- forgetting owner acknowledgements cannot be reconstructed), which matches
-- the standing policy that down migrations never fabricate data.

DROP TABLE IF EXISTS interaction_dag_training_deletion_ledger;
DROP TABLE IF EXISTS interaction_dag_training_execution;
DROP TABLE IF EXISTS interaction_dag_training_manifest_item;
DROP TABLE IF EXISTS interaction_dag_training_manifest;
DROP TABLE IF EXISTS interaction_dag_training_sample;
DROP TABLE IF EXISTS interaction_dag_training_policy;
DROP TABLE IF EXISTS interaction_dag_training_grant;

-- Restore the migration 449 reason check. training_replay rows created under
-- 472 belong to the dropped replay feature; remap them to the pre-existing
-- 'training' reason BEFORE the narrowing so the restored check cannot fail
-- on live rows (task #97 ordering rule: remap first, then narrow).
UPDATE agent_inbox_event SET reason = 'training' WHERE reason = 'training_replay';
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','environment_dispatch','memory_curation',
    'reminder','channel_role_changed','goal_graph_delta','goal_controller',
    'note_worker'
  ));

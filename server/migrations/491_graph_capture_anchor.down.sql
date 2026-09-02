-- Down for 491: restore the 487 reason check. graph_capture anchors are pure
-- record rows; remap them to the closest surviving reason BEFORE narrowing so
-- the restored check cannot fail on live rows, and so task_message /
-- interaction_dag_segment references stay intact (standing policy: down
-- migrations never delete data).

UPDATE agent_inbox_event SET reason = 'channel_message' WHERE reason = 'graph_capture';

DROP INDEX IF EXISTS uq_agent_inbox_event_graph_capture_message;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','training_replay','environment_dispatch',
    'memory_curation','reminder','channel_role_changed','goal_graph_delta',
    'goal_controller','note_worker'
  ));

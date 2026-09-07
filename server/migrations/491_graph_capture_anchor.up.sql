-- Graph memory turn capture (issue #3943): the #2295 hard-cut removed the
-- task-shaped mention wakes, and with them the only interaction DAG record
-- points for ordinary channel conversation turns. Canonical Message delivery
-- became the sole chat receive path, so the capture hooks on that path mint a
-- pure-record anchor task per directed turn message: reason='graph_capture',
-- born terminal (status='acked', terminal_outcome='completed',
-- requires_wake=false). Born-terminal rows are invisible to the inbox drain,
-- the daemon ack path and the 2h sweeper — the anchor never schedules work.

-- 1. Admit the new reason (full list = 487's list + 'graph_capture').
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
    'goal_controller','note_worker','graph_capture'
  ));

-- 2. Idempotent capture: one anchor per (message, agent). Delivery replays and
--    agent transport retries re-run the mint hook; the partial unique index
--    turns the second insert into a no-op (same discipline as the steering
--    event's (run_id, message_id) conflict target).
CREATE UNIQUE INDEX uq_agent_inbox_event_graph_capture_message
  ON agent_inbox_event (source_message_id, agent_id)
  WHERE reason = 'graph_capture' AND source_message_id IS NOT NULL;

ALTER TABLE agent_task_transport_audit
  DROP CONSTRAINT IF EXISTS agent_task_transport_audit_action_check;

ALTER TABLE agent_task_transport_audit
  ADD CONSTRAINT agent_task_transport_audit_action_check
  CHECK (action IN ('message_send', 'message_react', 'message_read', 'message_search', 'thread_unfollow'));

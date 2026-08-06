CREATE INDEX IF NOT EXISTS idx_agent_task_queue_activity_feed
  ON agent_task_queue(agent_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_chat_message_task_assistant_created
  ON chat_message(task_id, created_at DESC)
  WHERE role = 'assistant' AND content <> '';

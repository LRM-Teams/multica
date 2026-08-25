ALTER TABLE graph_memory_agent_tool_operation
  DROP COLUMN IF EXISTS completed_at,
  DROP COLUMN IF EXISTS status;

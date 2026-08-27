ALTER TABLE channel
  DROP COLUMN IF EXISTS graph_memory_agent_thinking_override,
  DROP COLUMN IF EXISTS graph_memory_agent_model_override,
  DROP COLUMN IF EXISTS graph_memory_agent_runtime_id_override;

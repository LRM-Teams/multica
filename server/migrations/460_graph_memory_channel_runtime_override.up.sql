-- Per-channel Graph Memory Agent execution override.
-- NULL runtime means inherit the workspace graph_memory_profile tuple.
ALTER TABLE channel
  ADD COLUMN graph_memory_agent_runtime_id_override uuid
    REFERENCES agent_runtime(id) ON DELETE SET NULL,
  ADD COLUMN graph_memory_agent_model_override text,
  ADD COLUMN graph_memory_agent_thinking_override text;

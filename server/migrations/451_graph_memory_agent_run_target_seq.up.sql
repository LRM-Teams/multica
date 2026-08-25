ALTER TABLE graph_memory_agent_run
  ADD COLUMN IF NOT EXISTS target_seq bigint NOT NULL DEFAULT 0 CHECK (target_seq >= 0);

UPDATE graph_memory_agent_run run
SET target_seq = state.consumed_seq
FROM graph_memory_agent_state state
WHERE state.channel_id = run.channel_id;

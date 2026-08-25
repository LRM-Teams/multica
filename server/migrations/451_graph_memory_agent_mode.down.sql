DROP INDEX IF EXISTS graph_memory_agent_citation_message_idx;
DROP TABLE IF EXISTS graph_memory_agent_citation;
DROP TABLE IF EXISTS graph_memory_agent_tool_operation;
DROP TABLE IF EXISTS graph_memory_agent_steering_event;
DROP TABLE IF EXISTS graph_memory_agent_trajectory;
ALTER TABLE graph_memory_agent_state DROP CONSTRAINT IF EXISTS graph_memory_agent_state_active_run_id_fkey;
DROP INDEX IF EXISTS graph_memory_agent_run_workspace_started_idx;
DROP INDEX IF EXISTS graph_memory_agent_run_one_active_idx;
DROP TABLE IF EXISTS graph_memory_agent_run;
DROP TABLE IF EXISTS graph_memory_agent_state;
DROP INDEX IF EXISTS graph_memory_channel_agent_workspace_status_idx;
DROP TABLE IF EXISTS graph_memory_channel_agent;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role = 'research_fleet');

ALTER TABLE channel DROP COLUMN IF EXISTS graph_memory_mode_override;

ALTER TABLE graph_memory_profile
  DROP COLUMN IF EXISTS memory_agent_max_tokens_per_hour,
  DROP COLUMN IF EXISTS memory_agent_max_continuous_turn_seconds,
  DROP COLUMN IF EXISTS memory_agent_max_nodes_per_minute,
  DROP COLUMN IF EXISTS memory_agent_max_nodes_per_call,
  DROP COLUMN IF EXISTS memory_agent_idle_grace_seconds,
  DROP COLUMN IF EXISTS consolidation_ttt_enabled,
  DROP COLUMN IF EXISTS recall_ttt_enabled,
  DROP COLUMN IF EXISTS memory_agent_thinking,
  DROP COLUMN IF EXISTS memory_agent_model,
  DROP COLUMN IF EXISTS memory_agent_runtime_id,
  DROP COLUMN IF EXISTS graph_memory_mode;

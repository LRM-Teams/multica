-- These foreign keys participate in channel deletion's cascade closure. Keep
-- each child lookup indexed so deleting a channel does not scan the graph
-- memory tables once per parent row.
CREATE INDEX IF NOT EXISTS graph_memory_agent_citation_channel_idx
  ON graph_memory_agent_citation(channel_id);
CREATE INDEX IF NOT EXISTS graph_memory_agent_steering_message_idx
  ON graph_memory_agent_steering_event(message_id);
CREATE INDEX IF NOT EXISTS graph_memory_agent_steering_trajectory_idx
  ON graph_memory_agent_steering_event(trajectory_id);
CREATE INDEX IF NOT EXISTS graph_memory_agent_state_active_run_idx
  ON graph_memory_agent_state(active_run_id);

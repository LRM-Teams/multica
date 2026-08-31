-- Migration 451 added the graph memory agent tables inside the channel delete
-- cascade closure but left four FK columns without a supporting index, so
-- cmd/migrate/channel_delete_fk_indexes_test.go fails and channel/message
-- deletes degrade into sequential scans on these tables. Same rationale and
-- style as migration 430.

CREATE INDEX IF NOT EXISTS idx_graph_memory_agent_citation_channel_id
  ON graph_memory_agent_citation (channel_id);
CREATE INDEX IF NOT EXISTS idx_graph_memory_agent_steering_event_message_id
  ON graph_memory_agent_steering_event (message_id);
CREATE INDEX IF NOT EXISTS idx_graph_memory_agent_steering_event_trajectory_id
  ON graph_memory_agent_steering_event (trajectory_id);
CREATE INDEX IF NOT EXISTS idx_graph_memory_agent_state_active_run_id
  ON graph_memory_agent_state (active_run_id) WHERE active_run_id IS NOT NULL;

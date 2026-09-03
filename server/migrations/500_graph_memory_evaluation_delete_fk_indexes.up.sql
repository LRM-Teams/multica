-- Graph Memory evaluation 499 ledger FKs followed the earlier agent/channel
-- teardown index migrations. PostgreSQL checks each FK child relation during
-- CASCADE enforcement; these indexes prevent a sequential scan of the
-- episode table per deleted agent or channel. Keep this forward-only and
-- idempotent for retries (same convention as 498).

CREATE INDEX IF NOT EXISTS idx_graph_memory_evaluation_episode_primary_agent_id
    ON graph_memory_evaluation_episode (primary_agent_id);
CREATE INDEX IF NOT EXISTS idx_graph_memory_evaluation_episode_channel_id
    ON graph_memory_evaluation_episode (channel_id);

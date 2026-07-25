DROP INDEX IF EXISTS idx_agent_inbox_event_agent_dm_exchange;

ALTER TABLE agent_inbox_event
  DROP COLUMN IF EXISTS agent_dm_turn,
  DROP COLUMN IF EXISTS agent_dm_exchange_id;

DROP INDEX IF EXISTS idx_agent_dm_exchange_pair_updated;
DROP INDEX IF EXISTS idx_agent_dm_exchange_channel_updated;
DROP TABLE IF EXISTS agent_dm_exchange;
DROP TABLE IF EXISTS agent_dm_owner_control;
DROP TABLE IF EXISTS agent_dm_pair_control;

DROP INDEX IF EXISTS channel_parent_channel_id_idx;
DROP INDEX IF EXISTS channel_created_by_agent_id_idx;
DROP INDEX IF EXISTS channel_agent_coordination_request_unique;

ALTER TABLE channel
  DROP CONSTRAINT IF EXISTS channel_agent_temporary_metadata,
  DROP CONSTRAINT IF EXISTS channel_client_request_id_length,
  DROP CONSTRAINT IF EXISTS channel_coordination_purpose_length,
  DROP COLUMN IF EXISTS client_request_id,
  DROP COLUMN IF EXISTS coordination_purpose,
  DROP COLUMN IF EXISTS created_by_agent_id,
  DROP COLUMN IF EXISTS parent_channel_id,
  DROP COLUMN IF EXISTS temporary;

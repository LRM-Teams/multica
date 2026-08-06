ALTER TABLE channel
  ADD COLUMN temporary BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN parent_channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  ADD COLUMN created_by_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  ADD COLUMN coordination_purpose TEXT,
  ADD COLUMN client_request_id TEXT;

ALTER TABLE channel
  ADD CONSTRAINT channel_coordination_purpose_length
    CHECK (coordination_purpose IS NULL OR char_length(coordination_purpose) <= 120),
  ADD CONSTRAINT channel_client_request_id_length
    CHECK (client_request_id IS NULL OR char_length(client_request_id) BETWEEN 1 AND 200),
  ADD CONSTRAINT channel_agent_temporary_metadata
    CHECK (
      created_by_agent_id IS NULL
      OR (
        temporary = true
        AND client_request_id IS NOT NULL
      )
    );

CREATE UNIQUE INDEX channel_agent_coordination_request_unique
  ON channel(workspace_id, created_by_agent_id, client_request_id)
  WHERE created_by_agent_id IS NOT NULL AND client_request_id IS NOT NULL;

CREATE INDEX channel_parent_channel_id_idx
  ON channel(parent_channel_id)
  WHERE parent_channel_id IS NOT NULL;

CREATE INDEX channel_created_by_agent_id_idx
  ON channel(created_by_agent_id)
  WHERE created_by_agent_id IS NOT NULL;

ALTER TABLE agent
    ALTER COLUMN avatar_url DROP NOT NULL;

DROP TRIGGER IF EXISTS agent_assign_durable_avatar_on_insert ON agent;
DROP FUNCTION IF EXISTS agent_assign_durable_avatar_on_insert();

ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_avatar_source_check,
    DROP COLUMN IF EXISTS avatar_source;

DROP FUNCTION IF EXISTS default_agent_avatar_url(UUID);

CREATE OR REPLACE FUNCTION default_agent_avatar_url(agent_id UUID)
RETURNS TEXT
LANGUAGE SQL
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT format(
        '/agent-avatars/human-%s.jpg',
        lpad(((get_byte(decode(md5(agent_id::text), 'hex'), 0) % 24) + 1)::text, 2, '0')
    )
$$;

UPDATE agent
SET avatar_url = '/agent-avatars/' ||
    substring(avatar_url FROM '/agent-avatars/v1/(human-[0-9]{2}\.jpg)$')
WHERE avatar_source IN ('assigned', 'picked')
  AND avatar_url ~ '^https://cdn\.leagent\.me/agent-avatars/v1/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$';

-- Agents created after migration 314 have no legacy visual identity to
-- restore, so a rollback assigns one stable member of the old 24-face pool.
UPDATE agent
SET avatar_url = default_agent_avatar_url(id)
WHERE avatar_source IN ('assigned', 'picked')
  AND avatar_url ~ '^https://cdn\.leagent\.me/agent-avatars/v2/agent-(0[1-9]|1[0-5])\.png$';

-- Move durable Agent defaults from frontend-bundled files to immutable OSS/CDN
-- objects. Absolute URLs intentionally remain renderable by older desktop
-- clients whose relative-file resolver points at the API origin.
CREATE OR REPLACE FUNCTION default_agent_avatar_url(agent_id UUID)
RETURNS TEXT
LANGUAGE SQL
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT format(
        'https://cdn.leagent.me/agent-avatars/v2/agent-%s.png',
        lpad(((get_byte(decode(md5(agent_id::text), 'hex'), 0) % 15) + 1)::text, 2, '0')
    )
$$;

-- Uploaded avatars are never touched. Assigned and user-picked legacy presets
-- retain their exact image identity while moving to the OSS-backed v1 catalog.
UPDATE agent
SET avatar_url = 'https://cdn.leagent.me/agent-avatars/v1/' ||
    substring(avatar_url FROM '/agent-avatars/(human-[0-9]{2}\.jpg)$')
WHERE avatar_source IN ('assigned', 'picked')
  AND avatar_url ~ '^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$';

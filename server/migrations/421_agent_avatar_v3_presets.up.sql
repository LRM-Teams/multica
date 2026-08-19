-- Point newly assigned system faces at the immutable Botlab v3 catalog.
-- Existing v2 URLs stay as-is so already-created Agents keep their face.
CREATE OR REPLACE FUNCTION default_agent_avatar_url(agent_id UUID)
RETURNS TEXT
LANGUAGE SQL
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT format(
        'https://cdn.leagent.me/agent-avatars/v3/agent-%s.png',
        lpad(((get_byte(decode(md5(agent_id::text), 'hex'), 0) % 15) + 1)::text, 2, '0')
    )
$$;

-- System-assigned v2 faces move to the matching v3 object. User-picked and
-- uploaded URLs stay put so an explicit choice is not rewritten.
UPDATE agent
SET avatar_url = replace(avatar_url, '/agent-avatars/v2/', '/agent-avatars/v3/')
WHERE avatar_source = 'assigned'
  AND avatar_url ~ '^https://cdn\.leagent\.me/agent-avatars/v2/agent-(0[1-9]|1[0-5])\.png$';

-- Agent avatars are durable member identity, matching user.avatar_url.
-- The concrete preset URL is selected once at the write boundary and then
-- persisted; reads must never derive an avatar from a mutable pool.
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

ALTER TABLE agent
    ADD COLUMN avatar_source TEXT NOT NULL DEFAULT 'assigned',
    ADD COLUMN avatar_attachment_id UUID REFERENCES attachment(id) ON DELETE RESTRICT;

ALTER TABLE agent
    ADD CONSTRAINT agent_avatar_source_check
    CHECK (avatar_source IN ('assigned', 'picked', 'uploaded')),
    ADD CONSTRAINT agent_avatar_attachment_source_check
    CHECK ((avatar_source = 'uploaded') = (avatar_attachment_id IS NOT NULL)),
    ADD CONSTRAINT agent_avatar_attachment_unique
    UNIQUE (avatar_attachment_id);

-- Keep every insertion path on the same durable boundary. Production creates
-- use the generated query below, while migrations/tests/administrative tools
-- may insert agent rows directly; those paths must not be able to create a
-- post-migration NULL that only a render-time fallback can display.
CREATE OR REPLACE FUNCTION agent_assign_durable_avatar_on_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.avatar_url IS NULL OR btrim(NEW.avatar_url) = '' THEN
        NEW.avatar_url := default_agent_avatar_url(NEW.id);
        NEW.avatar_source := 'assigned';
        NEW.avatar_attachment_id := NULL;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER agent_assign_durable_avatar_on_insert
BEFORE INSERT ON agent
FOR EACH ROW
EXECUTE FUNCTION agent_assign_durable_avatar_on_insert();

-- Preserve every existing concrete value. Rows that never had one receive a
-- concrete assignment exactly once; every historical row is intentionally
-- labelled assigned rather than guessing at legacy intent.
UPDATE agent
SET avatar_url = default_agent_avatar_url(id)
WHERE avatar_url IS NULL OR btrim(avatar_url) = '';

ALTER TABLE agent
    ADD CONSTRAINT agent_avatar_url_nonblank_check
    CHECK (btrim(avatar_url) <> ''),
    ALTER COLUMN avatar_url SET NOT NULL;

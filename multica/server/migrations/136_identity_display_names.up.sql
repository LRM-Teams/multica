-- Split stable identity handles from mutable display names.
--
-- Existing rows only had `name`, which the UI treated as a display label.
-- Preserve that label in `display_name`, then rewrite `name` to a deterministic
-- handle. Agent names were already unique per workspace; after this migration
-- that same uniqueness constraint applies to handles.

ALTER TABLE "user"
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

ALTER TABLE agent
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

UPDATE "user"
SET display_name = name
WHERE display_name = '';

UPDATE agent
SET display_name = name
WHERE display_name = '';

ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_workspace_name_unique;

DO $$
DECLARE
    r RECORD;
    base_handle TEXT;
    candidate TEXT;
    suffix INT;
BEGIN
    CREATE TEMP TABLE _multica_user_handles (
        handle TEXT PRIMARY KEY
    ) ON COMMIT DROP;

    FOR r IN
        SELECT id, name, email
        FROM "user"
        ORDER BY created_at ASC, id ASC
    LOOP
        base_handle := trim(both '_' FROM regexp_replace(lower(coalesce(r.name, '')), '[^a-z0-9]+', '_', 'g'));
        IF base_handle = '' THEN
            base_handle := trim(both '_' FROM regexp_replace(lower(split_part(r.email, '@', 1)), '[^a-z0-9]+', '_', 'g'));
        END IF;
        IF base_handle = '' THEN
            base_handle := 'user_' || substring(replace(r.id::text, '-', '') FROM 1 FOR 8);
        END IF;

        candidate := base_handle;
        suffix := 2;
        WHILE EXISTS (SELECT 1 FROM _multica_user_handles WHERE handle = candidate) LOOP
            candidate := base_handle || '_' || suffix::text;
            suffix := suffix + 1;
        END LOOP;

        INSERT INTO _multica_user_handles(handle) VALUES (candidate);
        UPDATE "user" SET name = candidate WHERE id = r.id;
    END LOOP;
END $$;

DO $$
DECLARE
    r RECORD;
    base_handle TEXT;
    candidate TEXT;
    suffix INT;
BEGIN
    CREATE TEMP TABLE _multica_agent_handles (
        workspace_id UUID NOT NULL,
        handle TEXT NOT NULL,
        PRIMARY KEY (workspace_id, handle)
    ) ON COMMIT DROP;

    FOR r IN
        SELECT id, workspace_id, name
        FROM agent
        ORDER BY workspace_id ASC, created_at ASC, id ASC
    LOOP
        base_handle := trim(both '_' FROM regexp_replace(lower(coalesce(r.name, '')), '[^a-z0-9]+', '_', 'g'));
        IF base_handle = '' THEN
            base_handle := 'agent_' || substring(replace(r.id::text, '-', '') FROM 1 FOR 8);
        END IF;

        candidate := base_handle;
        suffix := 2;
        WHILE EXISTS (
            SELECT 1 FROM _multica_agent_handles
            WHERE workspace_id = r.workspace_id AND handle = candidate
        ) LOOP
            candidate := base_handle || '_' || suffix::text;
            suffix := suffix + 1;
        END LOOP;

        INSERT INTO _multica_agent_handles(workspace_id, handle) VALUES (r.workspace_id, candidate);
        UPDATE agent SET name = candidate WHERE id = r.id;
    END LOOP;
END $$;

CREATE UNIQUE INDEX user_name_unique ON "user" (name);

ALTER TABLE agent
    ADD CONSTRAINT agent_workspace_name_unique UNIQUE (workspace_id, name);

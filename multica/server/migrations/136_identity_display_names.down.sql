DROP INDEX IF EXISTS user_name_unique;

ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_workspace_name_unique;

UPDATE "user"
SET name = display_name
WHERE display_name <> '';

DO $$
DECLARE
    r RECORD;
    base_name TEXT;
    candidate TEXT;
    suffix INT;
BEGIN
    CREATE TEMP TABLE _multica_agent_names (
        workspace_id UUID NOT NULL,
        name TEXT NOT NULL,
        PRIMARY KEY (workspace_id, name)
    ) ON COMMIT DROP;

    FOR r IN
        SELECT id, workspace_id, name, display_name
        FROM agent
        ORDER BY workspace_id ASC, created_at ASC, id ASC
    LOOP
        base_name := trim(coalesce(NULLIF(r.display_name, ''), r.name, ''));
        IF base_name = '' THEN
            base_name := 'Agent';
        END IF;

        candidate := base_name;
        suffix := 2;
        WHILE EXISTS (
            SELECT 1 FROM _multica_agent_names
            WHERE workspace_id = r.workspace_id AND name = candidate
        ) LOOP
            candidate := base_name || ' ' || suffix::text;
            suffix := suffix + 1;
        END LOOP;

        INSERT INTO _multica_agent_names(workspace_id, name) VALUES (r.workspace_id, candidate);
        UPDATE agent SET name = candidate WHERE id = r.id;
    END LOOP;
END $$;

ALTER TABLE agent
    ADD CONSTRAINT agent_workspace_name_unique UNIQUE (workspace_id, name);

ALTER TABLE "user"
    DROP COLUMN display_name;

ALTER TABLE agent
    DROP COLUMN display_name;

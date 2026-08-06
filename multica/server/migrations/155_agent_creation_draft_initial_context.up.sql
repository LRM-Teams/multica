ALTER TABLE agent_creation_draft
    ADD COLUMN initial_notes JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN initial_memory JSONB NOT NULL DEFAULT '{}'::jsonb;

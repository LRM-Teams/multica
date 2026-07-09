ALTER TABLE agent_creation_draft
    DROP COLUMN IF EXISTS initial_memory,
    DROP COLUMN IF EXISTS initial_notes;

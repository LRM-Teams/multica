ALTER TABLE agent_start_intent
    DROP COLUMN IF EXISTS reported_at,
    DROP COLUMN IF EXISTS lifecycle_seq;

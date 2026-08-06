-- Start-intent reports are ordered by the Computer-owned lifecycle sequence.
-- This makes duplicate and delayed accepted/ready/failed reports idempotent
-- without allowing an older observation to overwrite a newer one.
ALTER TABLE agent_start_intent
    ADD COLUMN lifecycle_seq BIGINT NOT NULL DEFAULT 0 CHECK (lifecycle_seq >= 0),
    ADD COLUMN reported_at TIMESTAMPTZ;

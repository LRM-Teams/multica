BEGIN;

-- Restore the unused cloud fence schema for a database rollback. Current
-- application code does not read or write these values.
CREATE TABLE IF NOT EXISTS computer_generation (
    daemon_id   TEXT PRIMARY KEY CHECK (btrim(daemon_id) <> ''),
    generation  BIGINT NOT NULL CHECK (generation > 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE daemon_heartbeat
    ADD COLUMN IF NOT EXISTS computer_generation BIGINT NOT NULL DEFAULT 0;

COMMIT;

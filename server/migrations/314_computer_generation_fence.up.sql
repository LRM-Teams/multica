BEGIN;

-- #2494 deployment/apply notes:
-- - Additive and backward compatible. Apply before binaries that send
--   X-Computer-Generation or Computer-level heartbeats.
-- - Old daemons remain accepted until a new resident claims generation > 0;
--   after that, missing/lower generations fail closed for that Computer.
-- - Workspace attestation columns are populated by the new server and ignored
--   by old clients. Runtime attestation remains a compatibility projection.
CREATE TABLE computer_generation (
    daemon_id   TEXT PRIMARY KEY CHECK (btrim(daemon_id) <> ''),
    generation  BIGINT NOT NULL CHECK (generation > 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE daemon_heartbeat
    ADD COLUMN computer_generation BIGINT NOT NULL DEFAULT 0;

ALTER TABLE machine_upgrade
    ADD COLUMN accepted_workspace_ids UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN attested_workspace_ids UUID[] NOT NULL DEFAULT '{}';

COMMIT;

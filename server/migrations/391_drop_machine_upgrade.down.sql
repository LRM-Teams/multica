BEGIN;

-- History is not reconstructed. Recreate an empty table so a down
-- migration can run; current application code does not read it.
CREATE TABLE IF NOT EXISTS machine_upgrade (
    id TEXT PRIMARY KEY,
    daemon_id TEXT NOT NULL CHECK (btrim(daemon_id) <> ''),
    requested_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL UNIQUE CHECK (btrim(request_id) <> ''),
    requested_target TEXT NOT NULL CHECK (btrim(requested_target) <> ''),
    resolved_target TEXT,
    phase TEXT NOT NULL DEFAULT 'starting',
    result TEXT,
    error_code TEXT,
    error_message TEXT,
    accepted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;

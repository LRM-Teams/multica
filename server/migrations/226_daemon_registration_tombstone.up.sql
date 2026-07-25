-- Permanently removed local computers must not recreate their runtime rows
-- through the daemon registration heartbeat. The tombstone is intentionally
-- retained after runtime cleanup; reconnecting requires a new daemon identity.
CREATE TABLE daemon_registration_tombstone (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    daemon_id TEXT NOT NULL,
    removed_by UUID NOT NULL,
    removed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, daemon_id),
    CONSTRAINT daemon_registration_tombstone_normalized_daemon_id
        CHECK (daemon_id <> '' AND daemon_id = lower(btrim(daemon_id)))
);

-- 307: Computer Workspace Execution Bindings
--
-- One row per (computer <=> workspace) execution authorization. Keyed by the
-- machine-wide Computer identity (daemon_id) and the immutable workspace UUID,
-- never by a slug. The execution credential is stored only as a hash; the raw
-- credential is issued to the Computer once and never persisted in the DB.
--
-- Deployment notes (forward):
--   - Additive, non-destructive. Run as part of the normal migrate-up roll.
--   - Idempotent guard: CREATE TABLE IF NOT EXISTS + unique key on
--     (daemon_id, workspace_id); re-runs are safe.
--   - Requires the `workspace` and `user` tables (present since 001).
--
-- Deployment notes (backward):
--   - Drops only this table; no data in other tables is touched.
--   - Safe to run before or after a forward on any replica; table is
--     re-created on the next forward.

CREATE TABLE IF NOT EXISTS computer_workspace_bindings (
    daemon_id          TEXT        NOT NULL,
    workspace_id       UUID        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id            UUID        NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    execution_token_hash TEXT      NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at         TIMESTAMPTZ,
    active             BOOLEAN     NOT NULL DEFAULT TRUE,
    PRIMARY KEY (daemon_id, workspace_id)
);

-- A binding is either active (never revoked) or revoked; there is no
-- half-life. Revoking is the idempotent way to remove execution authority.
ALTER TABLE computer_workspace_bindings
    DROP CONSTRAINT IF EXISTS computer_workspace_bindings_revocation_check;
ALTER TABLE computer_workspace_bindings
    ADD CONSTRAINT computer_workspace_bindings_revocation_check CHECK (
        (active = TRUE  AND revoked_at IS NULL) OR
        (active = FALSE AND revoked_at IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS computer_workspace_bindings_workspace_idx
    ON computer_workspace_bindings (workspace_id);

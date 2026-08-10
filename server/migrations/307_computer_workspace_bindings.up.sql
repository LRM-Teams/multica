-- 307: Computer Workspace Execution Bindings
--
-- One immutable owner per machine-wide Computer identity, plus one row per
-- (computer <=> workspace) execution authorization. Bindings are keyed by the
-- machine-wide Computer identity (daemon_id) and the immutable workspace UUID,
-- never by a slug. The execution credential is stored only as a hash; the raw
-- credential is issued to the Computer once and never persisted in the DB.
--
-- Deployment notes (forward):
--   - Additive, non-destructive. Run as part of the normal migrate-up roll.
--   - Idempotent guards: CREATE TABLE IF NOT EXISTS + unique keys on daemon_id
--     ownership and (daemon_id, workspace_id); re-runs are safe.
--   - Requires the `workspace` and `user` tables (present since 001).
--
-- Deployment notes (backward):
--   - Drops only these Computer tables; no data in other tables is touched.
--   - Safe to run before or after a forward on any replica; table is
--     re-created on the next forward.

CREATE TABLE IF NOT EXISTS computer_identity_owner (
    daemon_id          TEXT        PRIMARY KEY,
    user_id            UUID        NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

-- Backfill ownership if this additive migration is re-applied after bindings
-- were written by a pre-owner-table development build. Conflicting owners are
-- rejected instead of silently choosing one and transferring a Computer.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM computer_workspace_bindings
         GROUP BY daemon_id
        HAVING count(DISTINCT user_id) > 1
    ) THEN
        RAISE EXCEPTION 'computer identity has bindings owned by multiple users';
    END IF;
END
$$;

INSERT INTO computer_identity_owner (daemon_id, user_id)
SELECT daemon_id, min(user_id::text)::uuid
  FROM computer_workspace_bindings
 GROUP BY daemon_id
ON CONFLICT (daemon_id) DO NOTHING;

-- ON CONFLICT above is deliberately non-destructive. Verify that a pre-existing
-- owner row agrees with every backfilled connection instead of silently
-- retaining an ownership mismatch.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM computer_workspace_bindings b
          JOIN computer_identity_owner o ON o.daemon_id = b.daemon_id
         WHERE o.user_id <> b.user_id
    ) THEN
        RAISE EXCEPTION 'computer identity owner conflicts with workspace connection owner';
    END IF;
END
$$;

-- 316: Repair Computer Workspace binding schema after a seeded environment
-- recorded migration 307 without retaining its tables.
--
-- Deployment/apply notes:
--   - Additive and idempotent on databases where migration 307 is intact.
--   - Required before accepting Computer Workspace connections.
--   - The ownership consistency checks fail closed rather than choosing an
--     owner when existing binding data disagrees.

CREATE TABLE IF NOT EXISTS computer_identity_owner (
    daemon_id          TEXT        PRIMARY KEY,
    user_id            UUID        NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS computer_workspace_bindings (
    daemon_id            TEXT        NOT NULL,
    workspace_id         UUID        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id              UUID        NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    execution_token_hash TEXT        NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at           TIMESTAMPTZ,
    active               BOOLEAN     NOT NULL DEFAULT TRUE,
    PRIMARY KEY (daemon_id, workspace_id)
);

ALTER TABLE computer_workspace_bindings
    DROP CONSTRAINT IF EXISTS computer_workspace_bindings_revocation_check;
ALTER TABLE computer_workspace_bindings
    ADD CONSTRAINT computer_workspace_bindings_revocation_check CHECK (
        (active = TRUE  AND revoked_at IS NULL) OR
        (active = FALSE AND revoked_at IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS computer_workspace_bindings_workspace_idx
    ON computer_workspace_bindings (workspace_id);

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

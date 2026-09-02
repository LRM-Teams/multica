-- Task 17: versioned retention policy, encrypted archive manifest, restore
-- lease/audit and sweep cursor (spec §13).
--
--   * The policy is VERSIONED per workspace (append-only rows; current =
--     MAX(version)). The DB CHECKs are the platform shadow caps: trajectory
--     hot <= 90 days, encrypted archive <= 365 days, full trace hot <= 30
--     days. A workspace may shorten; nothing can lengthen past a cap here.
--   * Every existing workspace binds to the explicit bootstrap version 1
--     (90/365/30) at migration time — no default silently creates or
--     lengthens retention.
--   * The archive manifest records the encrypted object only: workspace
--     key envelope, ciphertext sha256 and size. erase_due_at is bound at
--     archive time and may only ever tighten (LEAST), never extend.
--   * Restore leases are the object-scoped audit: actor, reason, TTL,
--     manifest. They never re-enter Search/index state.

-- 1) Versioned per-workspace retention policy.
CREATE TABLE memory_retention_policy (
    workspace_id        uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    version             bigint      NOT NULL CHECK (version > 0),
    trajectory_hot_days int         NOT NULL CHECK (trajectory_hot_days > 0 AND trajectory_hot_days <= 90),
    archive_days        int         NOT NULL CHECK (archive_days > 0 AND archive_days <= 365),
    trace_hot_days      int         NOT NULL CHECK (trace_hot_days > 0 AND trace_hot_days <= 30),
    updated_by          text        NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, version)
);

-- Bootstrap contract: existing workspaces bind to explicit version 1
-- (90/365/30 shadow defaults) rather than relying on runtime defaults.
INSERT INTO memory_retention_policy (workspace_id, version, trajectory_hot_days, archive_days, trace_hot_days, updated_by)
SELECT id, 1, 90, 365, 30, 'migration:471'
FROM workspace
ON CONFLICT DO NOTHING;

-- 2) Encrypted archive manifest (spec §13: manifest holds ref/hash; the
--    hot body retires only after the ciphertext hash is recorded).
CREATE TABLE memory_archive_manifest (
    id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    blob_id        uuid        NOT NULL REFERENCES graph_memory_blob(id) ON DELETE CASCADE,
    object_ref     text        NOT NULL,
    key_envelope   text        NOT NULL,
    cipher_sha256  text        NOT NULL,
    size_bytes     bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    status         text        NOT NULL DEFAULT 'archived' CHECK (status IN ('archived', 'erased')),
    archived_at    timestamptz NOT NULL DEFAULT now(),
    erase_due_at   timestamptz NOT NULL,
    erased_at      timestamptz,
    UNIQUE (workspace_id, blob_id)
);
CREATE INDEX memory_archive_manifest_due_idx
    ON memory_archive_manifest (erase_due_at)
    WHERE status = 'archived';

-- 3) Restore lease + audit (spec AC 41: current ACL, explicit reason,
--    short TTL, object-scoped audit). Rows are retained as the audit
--    record; expires_at is the lease TTL.
CREATE TABLE memory_archive_restore_lease (
    id           uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    manifest_id  uuid        NOT NULL REFERENCES memory_archive_manifest(id) ON DELETE CASCADE,
    actor        text        NOT NULL,
    reason       text        NOT NULL CHECK (reason <> ''),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_archive_restore_lease_manifest_idx
    ON memory_archive_restore_lease (manifest_id, created_at DESC);

-- 4) Sweep cursor: idempotent retention sweeps per workspace per stream.
CREATE TABLE memory_retention_sweep_cursor (
    workspace_id            uuid        NOT NULL PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    last_trajectory_sweep_at timestamptz,
    last_trace_sweep_at     timestamptz,
    last_archive_sweep_at   timestamptz,
    updated_at              timestamptz NOT NULL DEFAULT now()
);

-- Task 16: online channel-owned Graph migration (spec §12).
--
-- Invariants:
--   * channel.project_id can only change inside a transaction that also
--     wrote a graph_memory_channel_binding row — the binding service's CAS
--     generation. A row-level guard rejects every future direct writer that
--     skips the service (settings UI, goal bootstrap, accidental SQL).
--   * graph_memory_channel_migration_state is the copy ledger: the binding
--     transaction records the source watermark; the migration worker copies
--     channel-owned artifacts up to that watermark, then flips the phase.
--   * graph_memory_migration_redirect is the citation ledger: old canonical
--     refs resolve to their new copies (readers redirect; nothing rewrites
--     historical manifests).
--   * graph_memory_migration_blob_ref records blob refs added to the new
--     projection — blob bytes are never duplicated.
--   * The copy route is DB-default OFF like every other memory phase gate:
--     memory_read_phase_gate.channel_migration_enabled.
BEGIN;

-- 1) Phase gate for the migration worker route.
ALTER TABLE memory_read_phase_gate
    ADD COLUMN channel_migration_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE memory_read_phase_gate
    DROP CONSTRAINT memory_read_phase_gate_transition_check;
ALTER TABLE memory_read_phase_gate
    ADD CONSTRAINT memory_read_phase_gate_transition_check CHECK (
        (atoms_enabled OR search_v2_enabled OR explore_enabled
         OR citations_enabled OR atom_consolidation_enabled
         OR channel_migration_enabled)
        <= retraction_canary_ok
    );

-- 2) Binding generations: one row per service-mediated binding change. The
--    txid column doubles as the same-transaction marker the guard checks.
CREATE TABLE graph_memory_channel_binding (
    id             uuid NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL,
    channel_id     uuid NOT NULL,
    generation     bigint NOT NULL CHECK (generation > 0),
    old_project_id uuid,
    new_project_id uuid,
    route_kind     text NOT NULL CHECK (route_kind IN ('project', 'channel')),
    route_owner_id uuid NOT NULL,
    route_generation bigint NOT NULL,
    source_watermark bigint NOT NULL DEFAULT 0,
    actor          text NOT NULL DEFAULT '',
    txid           bigint NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT graph_memory_channel_binding_gen_unique UNIQUE (channel_id, generation)
);
CREATE INDEX graph_memory_channel_binding_channel_idx
    ON graph_memory_channel_binding (channel_id, generation DESC);

-- 3) Copy ledger: the worker's idempotent state machine per binding.
CREATE TABLE graph_memory_channel_migration_state (
    workspace_id uuid NOT NULL,
    channel_id   uuid NOT NULL,
    binding_generation bigint NOT NULL,
    phase        text NOT NULL CHECK (phase IN ('pending', 'copying', 'completed', 'aborted')),
    source_watermark bigint NOT NULL DEFAULT 0,
    copied_atoms  integer NOT NULL DEFAULT 0,
    copied_nodes  integer NOT NULL DEFAULT 0,
    copied_edges  integer NOT NULL DEFAULT 0,
    error         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, binding_generation)
);
CREATE INDEX graph_memory_channel_migration_pending_idx
    ON graph_memory_channel_migration_state (phase, created_at)
    WHERE phase IN ('pending', 'copying');

-- 4) Citation redirect ledger: old canonical id -> new canonical id. Old
--    refs become unsearchable tombstones by resolution, not by rewriting
--    write-once rows or historical manifests.
CREATE TABLE graph_memory_migration_redirect (
    workspace_id uuid NOT NULL,
    old_kind     text NOT NULL CHECK (old_kind IN ('atom', 'node', 'edge')),
    old_id       text NOT NULL,
    new_kind     text NOT NULL CHECK (new_kind IN ('atom', 'node', 'edge')),
    new_id       text NOT NULL,
    binding_generation bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, old_kind, old_id),
    CONSTRAINT graph_memory_migration_redirect_pair CHECK (old_kind != new_kind OR old_id != new_id)
);
CREATE INDEX graph_memory_migration_redirect_new_idx
    ON graph_memory_migration_redirect (workspace_id, new_kind, new_id);

-- 5) Blob refs gained by the new projection (bytes are never copied).
CREATE TABLE graph_memory_migration_blob_ref (
    workspace_id uuid NOT NULL,
    channel_id   uuid NOT NULL,
    binding_generation bigint NOT NULL,
    blob_ref     text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, channel_id, binding_generation, blob_ref)
);

-- 6) The binding guard: channel.project_id changes are legal only in a
--    transaction that wrote the matching binding-generation row.
CREATE OR REPLACE FUNCTION graph_memory_channel_binding_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.project_id IS DISTINCT FROM OLD.project_id THEN
        IF NOT EXISTS (
            SELECT 1 FROM graph_memory_channel_binding b
            WHERE b.channel_id = NEW.id
              AND b.txid = pg_current_xact_id()::text::bigint
        ) THEN
            RAISE EXCEPTION
                'channel project binding must go through ChannelProjectBindingService (channel %)',
                NEW.id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_channel_binding_guard_trigger
BEFORE UPDATE OF project_id ON channel
FOR EACH ROW EXECUTE FUNCTION graph_memory_channel_binding_guard();

COMMIT;

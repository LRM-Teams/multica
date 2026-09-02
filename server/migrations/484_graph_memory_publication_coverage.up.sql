-- Task 14: DB-authoritative Graph publication ledger.
--
-- The file-store `current` pointer becomes a recoverable cache/projection.
-- Reader authority moves to graph_memory_publication(_index): a publication
-- commits generation + immutable file manifest hash + coverage/provenance
-- ledgers in ONE PostgreSQL transaction that locks every contributing
-- memory_source_guard FOR KEY SHARE and rechecks ACL/scope/retraction before
-- the CAS. Deletion keeps taking FOR UPDATE on the same keys in the same
-- source-key order, so delete-first aborts the publication and publish-first
-- makes the deletion wait and then quarantine the newly published closure.
--
-- The consolidation route joins the default-off phase gate set: the
-- scheduler/manual consolidation only claims work while
-- atom_consolidation_enabled is green.

-- 1) atom_consolidation route on the phase gate (default off).
ALTER TABLE memory_read_phase_gate
    ADD COLUMN atom_consolidation_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE memory_read_phase_gate
    DROP CONSTRAINT memory_read_phase_gate_transition_check;
ALTER TABLE memory_read_phase_gate
    ADD CONSTRAINT memory_read_phase_gate_transition_check CHECK (
        (atoms_enabled OR search_v2_enabled OR explore_enabled
         OR citations_enabled OR atom_consolidation_enabled)
        <= retraction_canary_ok
    );

-- 2) Current generation per graph scope — the CAS authority. One row per
--    (workspace, graph); publication advances current_generation only when
--    it still holds the base generation it planned against.
CREATE TABLE graph_memory_publication (
    workspace_id      uuid NOT NULL,
    graph_kind        text NOT NULL CHECK (graph_kind IN ('project', 'channel')),
    graph_owner_id    uuid NOT NULL,
    current_generation bigint NOT NULL CHECK (current_generation > 0),
    graph_version     integer NOT NULL CHECK (graph_version > 0),
    file_manifest_hash text NOT NULL CHECK (file_manifest_hash <> ''),
    published_at      timestamptz NOT NULL DEFAULT now(),
    published_by      text NOT NULL DEFAULT 'consolidator',
    PRIMARY KEY (workspace_id, graph_kind, graph_owner_id),
    CONSTRAINT graph_memory_publication_scope_fkey
        FOREIGN KEY (workspace_id)
        REFERENCES workspace (id) ON DELETE CASCADE
);

-- 3) Reader-facing active index pointer. Written in the same transaction as
--    the publication CAS so a reader that observes the row observes the
--    complete generation.
CREATE TABLE graph_memory_publication_index (
    workspace_id    uuid NOT NULL,
    graph_kind      text NOT NULL CHECK (graph_kind IN ('project', 'channel')),
    graph_owner_id  uuid NOT NULL,
    active_generation bigint NOT NULL CHECK (active_generation > 0),
    graph_version   integer NOT NULL CHECK (graph_version > 0),
    file_manifest_hash text NOT NULL,
    activated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, graph_kind, graph_owner_id),
    CONSTRAINT graph_memory_publication_index_scope_fkey
        FOREIGN KEY (workspace_id)
        REFERENCES workspace (id) ON DELETE CASCADE
);

-- 4) Coverage ledger: the exact atom closure a generation consumed. A later
--    retraction joins against this to quarantine precisely the nodes that
--    carry deleted bodies.
CREATE TABLE graph_memory_publication_coverage (
    workspace_id  uuid NOT NULL,
    graph_kind    text NOT NULL CHECK (graph_kind IN ('project', 'channel')),
    graph_owner_id uuid NOT NULL,
    generation    bigint NOT NULL CHECK (generation > 0),
    atom_id       text NOT NULL,
    segment_id    text NOT NULL,
    covered_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, graph_kind, graph_owner_id, generation, atom_id),
    CONSTRAINT graph_memory_publication_coverage_scope_fkey
        FOREIGN KEY (workspace_id)
        REFERENCES workspace (id) ON DELETE CASCADE
);
CREATE INDEX graph_memory_publication_coverage_atom_idx
    ON graph_memory_publication_coverage (workspace_id, atom_id);

-- 5) Reverse provenance: node -> atoms/segments it was derived from, per
--    published generation.
CREATE TABLE graph_memory_publication_provenance (
    workspace_id  uuid NOT NULL,
    graph_kind    text NOT NULL CHECK (graph_kind IN ('project', 'channel')),
    graph_owner_id uuid NOT NULL,
    generation    bigint NOT NULL CHECK (generation > 0),
    node_id       text NOT NULL,
    atom_ids      text[] NOT NULL CHECK (array_length(atom_ids, 1) > 0),
    segment_ids   text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (workspace_id, graph_kind, graph_owner_id, generation, node_id),
    CONSTRAINT graph_memory_publication_provenance_scope_fkey
        FOREIGN KEY (workspace_id)
        REFERENCES workspace (id) ON DELETE CASCADE
);
CREATE INDEX graph_memory_publication_provenance_atom_idx
    ON graph_memory_publication_provenance (workspace_id, graph_kind, graph_owner_id, generation)
    WHERE array_length(atom_ids, 1) > 0;

-- 6) Per-generation outcome: what happened to each publication attempt,
--    including aborts (stale base generation, retracted source, manifest
--    mismatch). Aggregate counters only — never node bodies or payloads.
CREATE TABLE graph_memory_publication_outcome (
    workspace_id  uuid NOT NULL,
    graph_kind    text NOT NULL CHECK (graph_kind IN ('project', 'channel')),
    graph_owner_id uuid NOT NULL,
    generation    bigint NOT NULL CHECK (generation > 0),
    outcome       text NOT NULL CHECK (outcome IN (
        'published', 'aborted_stale_base', 'aborted_retracted_source',
        'aborted_manifest_mismatch')),
    graph_version integer NOT NULL DEFAULT 0,
    file_manifest_hash text NOT NULL DEFAULT '',
    covered_atom_count  integer NOT NULL DEFAULT 0,
    covered_segment_count integer NOT NULL DEFAULT 0,
    node_count    integer NOT NULL DEFAULT 0,
    source_keys   text[] NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, graph_kind, graph_owner_id, generation),
    CONSTRAINT graph_memory_publication_outcome_scope_fkey
        FOREIGN KEY (workspace_id)
        REFERENCES workspace (id) ON DELETE CASCADE
);

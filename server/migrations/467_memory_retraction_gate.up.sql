-- Task 8A: synchronous retraction fence, reverse provenance, and the
-- default-off read gates (spec §9/§13).
--
-- Invariants:
--   * one memory_source_guard row per canonical source, backfilled from
--     existing interaction_dag_segment rows before any mutation path can run;
--   * retraction_registry + memory_deletion_audit make every fence event
--     attributable (actor, reason) and auditable;
--   * memory_source_provenance is the pre-maintained reverse closure from a
--     canonical source to every published consumer (atoms); the Task 7
--     publish transaction upserts it;
--   * quarantined_pending_recompute records the downstream items a
--     retraction pulls out of readable state until recomputed;
--   * memory_read_phase_gate is DB-default OFF for every external memory
--     route: Atom reads, v2 Search, Explore, and citations stay unreachable
--     until an operator-approved gate transition flips them; only shadow
--     comparison jobs may run before that.

BEGIN;

-- One guard row per canonical source. Retracted sources fail closed on every
-- fenced reader.
CREATE TABLE memory_source_guard (
    workspace_id uuid NOT NULL,
    source_kind  text NOT NULL,
    source_id    text NOT NULL,
    retracted_at timestamptz,
    retracted_by text NOT NULL DEFAULT '',
    reason       text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, source_kind, source_id),
    CONSTRAINT memory_source_guard_kind_check CHECK (source_kind IN (
        'task_output', 'comment', 'channel_message', 'channel',
        'chat_session', 'attachment', 'issue', 'project', 'workspace',
        'env_dispatch', 'memory_agent_run'
    )),
    CONSTRAINT memory_source_guard_fence_shape_check
        CHECK ((retracted_at IS NULL) = (retracted_by = '' AND reason = ''))
);

-- Attributable retraction events; one event fences N sources.
CREATE TABLE retraction_registry (
    id           uuid NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    actor        text NOT NULL,
    reason       text NOT NULL DEFAULT '',
    source_count integer NOT NULL CHECK (source_count > 0),
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Pre-maintained reverse provenance: canonical source -> published
-- consumers. Written by the Task 7 publish transaction and by backfill.
CREATE TABLE memory_source_provenance (
    workspace_id   uuid NOT NULL,
    source_kind    text NOT NULL,
    source_id      text NOT NULL,
    consumer_kind  text NOT NULL,
    consumer_id    text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, source_kind, source_id, consumer_kind, consumer_id)
);
CREATE INDEX memory_source_provenance_source_idx
    ON memory_source_provenance (workspace_id, source_kind, source_id);

-- Downstream items pulled out of readable state by a retraction until they
-- are recomputed or dropped.
CREATE TABLE quarantined_pending_recompute (
    workspace_id  uuid NOT NULL,
    retraction_id uuid NOT NULL,
    consumer_kind text NOT NULL,
    consumer_id   text NOT NULL,
    quarantined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, consumer_kind, consumer_id, retraction_id)
);

-- Deletion audit: one row per fenced source per retraction event.
CREATE TABLE memory_deletion_audit (
    id               uuid NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     uuid NOT NULL,
    retraction_id    uuid NOT NULL,
    source_kind      text NOT NULL,
    source_id        text NOT NULL,
    quarantined_count integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_deletion_audit_retraction_idx
    ON memory_deletion_audit (workspace_id, retraction_id);

-- Phase gates: every external memory route is DB-default disabled. Flipping a
-- route on requires an explicit operator-approved UPDATE of this row.
CREATE TABLE memory_read_phase_gate (
    workspace_id    uuid NOT NULL PRIMARY KEY,
    atoms_enabled   boolean NOT NULL DEFAULT false,
    search_v2_enabled boolean NOT NULL DEFAULT false,
    explore_enabled boolean NOT NULL DEFAULT false,
    citations_enabled boolean NOT NULL DEFAULT false,
    retraction_canary_ok boolean NOT NULL DEFAULT false,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT memory_read_phase_gate_transition_check CHECK (
        (atoms_enabled OR search_v2_enabled OR explore_enabled OR citations_enabled)
        <= retraction_canary_ok
    )
);

-- Backfill guards for every canonical source that already published a
-- Segment (task_output keyed by the segment's agent_run_id). Runs before any
-- mutation path can observe the table set: the fence writers below only ever
-- upsert, so a source missing here is still fenced on first retraction.
INSERT INTO memory_source_guard (workspace_id, source_kind, source_id)
SELECT DISTINCT workspace_id, 'task_output', agent_run_id::text
FROM interaction_dag_segment
ON CONFLICT DO NOTHING;

-- Reverse provenance backfill: every published atom cites the segment's
-- task_output source.
INSERT INTO memory_source_provenance (workspace_id, source_kind, source_id, consumer_kind, consumer_id)
SELECT DISTINCT atom.workspace_id, 'task_output', segment.agent_run_id::text, 'graph_memory_atom', atom.atom_id
FROM graph_memory_atom atom
JOIN interaction_dag_segment segment
  ON segment.workspace_id = atom.workspace_id
 AND segment.segment_id = atom.segment_id
ON CONFLICT DO NOTHING;

COMMIT;

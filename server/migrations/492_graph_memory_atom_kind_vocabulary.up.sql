-- Slice 1.1 (plan §6, spec §4): widen the atom kind vocabulary to the
-- closed seven-kind set and add the explicit-decision audit table for the
-- retired legacy labels.
--
-- graph_memory_atom stays write-once: no existing row is rewritten here.
-- Existing rows can only contain the original three kinds (the prior CHECK
-- admitted nothing else), so the wider CHECK only opens the four new kinds
-- for future publishes. Retired labels ('rule', 'procedure') are rejected
-- at every boundary; converting legacy material requires an explicit
-- decision recorded in graph_memory_kind_backfill_audit (checkpoint and
-- reason), never a silent mapping, and never auto-generates a Skill
-- (ADR 0021 Decision 1, spec §4).
ALTER TABLE graph_memory_atom
    DROP CONSTRAINT graph_memory_atom_kind_check;

ALTER TABLE graph_memory_atom
    ADD CONSTRAINT graph_memory_atom_kind_check CHECK (
        kind IN ('fact', 'event', 'instruction', 'preference',
                 'decision', 'constraint', 'fallback')
    );

-- Audit trail for explicit legacy-kind conversions. One row per decision:
-- 'rule' records the deliberate instruction-vs-constraint choice;
-- 'procedure' records the outcome of candidate re-evaluation (one or more
-- current-kind atoms, or fallback). NodeRole needs no DB column: the graph
-- is the workspace file model (migration 481 comment, ADR 0021 Decision 6),
-- so role metadata persists in the versioned node files.
CREATE TABLE graph_memory_kind_backfill_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id uuid NOT NULL,
    source_kind text NOT NULL,
    target_kind text NOT NULL,
    action text NOT NULL,
    reason text NOT NULL,
    decided_by text NOT NULL,
    decided_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT graph_memory_kind_backfill_source_check
        CHECK (source_kind IN ('rule', 'procedure')),
    CONSTRAINT graph_memory_kind_backfill_target_check
        CHECK (target_kind IN ('fact', 'event', 'instruction', 'preference',
                               'decision', 'constraint', 'fallback')),
    CONSTRAINT graph_memory_kind_backfill_action_check
        CHECK (action IN ('explicit_choice', 'candidate_re_evaluation')),
    CONSTRAINT graph_memory_kind_backfill_rule_target_check
        CHECK (source_kind <> 'rule'
               OR target_kind IN ('instruction', 'constraint')),
    CONSTRAINT graph_memory_kind_backfill_reason_check
        CHECK (length(btrim(reason)) > 0)
);

CREATE INDEX graph_memory_kind_backfill_workspace_idx
    ON graph_memory_kind_backfill_audit (workspace_id, source_kind, decided_at);

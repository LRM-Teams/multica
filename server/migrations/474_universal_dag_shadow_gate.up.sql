-- Task 21: the audited shadow-gate phase registry (spec 15/16/19, AC51/52).
--
-- Invariants:
--   * every gate is DB-default disabled; the linear ladder
--     disabled -> shadow -> enabled is enforced by the service's audited CAS
--     transition, never by ad-hoc UPDATEs;
--   * universal_dag_gate_transition is the append-only audit ledger: every
--     promotion, auto-shutdown and synchronous failure demotion is recorded
--     with actor, reason, trigger, evidence snapshot and policy version;
--   * global-scope gates (reward shadow, tenant/pooled training) share the
--     table through the nil-uuid sentinel workspace; workspace-scope gates
--     (memory routes) key on the real workspace id;
--   * gate_version is the CAS token: a stale expected version affects zero
--     rows and the caller must treat it as a conflict, never a silent
--     overwrite.

BEGIN;

CREATE TABLE universal_dag_shadow_gate (
    scope          text        NOT NULL CHECK (scope IN ('workspace', 'global')),
    workspace_id   uuid        NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    gate_name      text        NOT NULL,
    phase          text        NOT NULL DEFAULT 'disabled'
      CHECK (phase IN ('disabled', 'shadow', 'enabled')),
    gate_version   bigint      NOT NULL DEFAULT 0 CHECK (gate_version >= 0),
    policy_version bigint      NOT NULL DEFAULT 0 CHECK (policy_version >= 0),
    evidence       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_by     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, workspace_id, gate_name),
    CONSTRAINT universal_dag_shadow_gate_scope_pairing CHECK (
        (scope = 'global'
         AND workspace_id = '00000000-0000-0000-0000-000000000000')
        OR
        (scope = 'workspace'
         AND workspace_id <> '00000000-0000-0000-0000-000000000000')
    ),
    CONSTRAINT universal_dag_shadow_gate_gate_names CHECK (gate_name IN (
        'atoms', 'search_v2', 'explore', 'citations',
        'atom_consolidation', 'channel_migration',
        'reward_shadow', 'tenant_training', 'pooled_training'
    ))
);
CREATE INDEX universal_dag_shadow_gate_workspace_idx
    ON universal_dag_shadow_gate (workspace_id)
    WHERE scope = 'workspace';

CREATE TABLE universal_dag_gate_transition (
    transition_id bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope         text        NOT NULL CHECK (scope IN ('workspace', 'global')),
    workspace_id  uuid        NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    gate_name     text        NOT NULL,
    from_phase    text        NOT NULL CHECK (from_phase IN ('disabled', 'shadow', 'enabled')),
    to_phase      text        NOT NULL CHECK (to_phase IN ('disabled', 'shadow', 'enabled')),
    reason        text        NOT NULL,
    trigger       text        NOT NULL DEFAULT 'manual'
      CHECK (trigger IN ('manual', 'auto_shutdown', 'failure')),
    evidence      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    policy_version bigint     NOT NULL DEFAULT 0,
    actor         text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX universal_dag_gate_transition_recent_idx
    ON universal_dag_gate_transition (workspace_id, created_at DESC);

COMMIT;

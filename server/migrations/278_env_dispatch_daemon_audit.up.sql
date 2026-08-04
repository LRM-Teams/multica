-- Opt-in, correlation-scoped evidence for env-dispatch daemon reclamation.
-- Resource and scope identities are intentionally snapshots rather than foreign
-- keys to project/channel/runtime/sandbox rows: those rows can be deleted before
-- the audit reaches a terminal verdict. The audit ledger FKs remain internal so
-- its run, resource, obligation, and event history are removed together.
-- No generic JSON payload is retained: the ledger stores only structured
-- identifiers, states, timestamps, and sanitized reason/error codes.

CREATE TABLE env_dispatch_audit_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    initiator_id UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    dispatch_type TEXT NOT NULL
        CHECK (dispatch_type IN ('issue', 'message')),
    primary_scope_id UUID NOT NULL,
    outcome TEXT NOT NULL DEFAULT 'running'
        CHECK (outcome IN (
            'running', 'succeeded', 'rejected', 'failed', 'timed_out',
            'cancelled', 'deleted'
        )),
    verdict TEXT NOT NULL DEFAULT 'pending'
        CHECK (verdict IN (
            'pending', 'no_leak_observed', 'leak_confirmed', 'inconclusive'
        )),
    reclamation_deadline TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (reclamation_deadline >= started_at),
    CHECK (completed_at IS NULL OR completed_at >= started_at)
);

CREATE INDEX env_dispatch_audit_run_workspace_started_idx
    ON env_dispatch_audit_run (workspace_id, started_at DESC, id DESC);

CREATE INDEX env_dispatch_audit_run_initiator_started_idx
    ON env_dispatch_audit_run (initiator_id, started_at DESC, id DESC);

CREATE TABLE env_dispatch_audit_resource (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_id UUID NOT NULL REFERENCES env_dispatch_audit_run(id) ON DELETE CASCADE,
    resource_kind TEXT NOT NULL
        CHECK (resource_kind IN (
            'sandbox', 'runtime', 'binding', 'derived_agent', 'task', 'session'
        )),
    resource_id TEXT NOT NULL CHECK (resource_id <> ''),
    daemon_id TEXT,
    environment_id UUID,
    project_id UUID,
    channel_id UUID,
    ownership_mode TEXT NOT NULL DEFAULT 'exclusive'
        CHECK (ownership_mode IN ('exclusive', 'shared')),
    owner_state TEXT NOT NULL DEFAULT 'unknown'
        CHECK (owner_state IN ('active', 'terminal', 'deleted', 'unknown')),
    classification TEXT NOT NULL DEFAULT 'pending'
        CHECK (classification IN (
            'pending', 'reclaimed', 'legitimately_active', 'unreclaimed',
            'inconclusive'
        )),
    first_observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_observed_at TIMESTAMPTZ,
    reclaimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT env_dispatch_audit_resource_identity_unique
        UNIQUE (audit_id, resource_kind, resource_id),
    CONSTRAINT env_dispatch_audit_resource_audit_id_id_unique
        UNIQUE (audit_id, id),
    CHECK (last_observed_at IS NULL OR last_observed_at >= first_observed_at),
    CHECK (reclaimed_at IS NULL OR reclaimed_at >= first_observed_at)
);

CREATE INDEX env_dispatch_audit_resource_audit_classification_idx
    ON env_dispatch_audit_resource (audit_id, classification, first_observed_at, id);

CREATE TABLE env_dispatch_reclamation_obligation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_resource_id UUID NOT NULL
        REFERENCES env_dispatch_audit_resource(id) ON DELETE CASCADE,
    trigger TEXT NOT NULL
        CHECK (trigger IN (
            'terminal', 'failure', 'timeout', 'cancellation', 'project_delete',
            'channel_delete', 'rollback'
        )),
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN (
            'pending', 'in_progress', 'succeeded', 'exhausted', 'not_required'
        )),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_code TEXT,
    next_attempt_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT env_dispatch_reclamation_obligation_resource_unique
        UNIQUE (audit_resource_id),
    CONSTRAINT env_dispatch_reclamation_obligation_error_code_sanitized
        CHECK (
            last_error_code IS NULL
            OR last_error_code ~ '^[a-z0-9][a-z0-9_.:/-]{0,127}$'
        ),
    CHECK (
        (state = 'pending' AND next_attempt_at IS NOT NULL)
        OR (state <> 'pending' AND next_attempt_at IS NULL)
    )
);

CREATE INDEX env_dispatch_reclamation_obligation_ready_idx
    ON env_dispatch_reclamation_obligation (next_attempt_at, id)
    WHERE state = 'pending';

CREATE TABLE env_dispatch_audit_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    audit_id UUID NOT NULL REFERENCES env_dispatch_audit_run(id) ON DELETE CASCADE,
    audit_resource_id UUID NOT NULL,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    event_type TEXT NOT NULL
        CHECK (event_type IN (
            'provisioned', 'binding_observed', 'creation_failed',
            'rollback_started', 'owner_terminal', 'ownership_deferred',
            'cleanup_requested', 'cleanup_attempted', 'cleanup_retry_scheduled',
            'cleanup_exhausted', 'runtime_offlined',
            'sandbox_deletion_requested', 'reclaimed', 'cleanup_failed',
            'observation_unavailable', 'classification_updated', 'dispatch_outcome'
        )),
    reason_code TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT env_dispatch_audit_event_sequence_unique UNIQUE (audit_id, sequence),
    CONSTRAINT env_dispatch_audit_event_resource_audit_fk
        FOREIGN KEY (audit_id, audit_resource_id)
        REFERENCES env_dispatch_audit_resource(audit_id, id) ON DELETE CASCADE,
    CONSTRAINT env_dispatch_audit_event_reason_code_sanitized
        CHECK (
            reason_code IS NULL
            OR reason_code ~ '^[a-z0-9][a-z0-9_.:/-]{0,127}$'
        )
);

CREATE INDEX env_dispatch_audit_event_audit_sequence_idx
    ON env_dispatch_audit_event (audit_id, sequence);

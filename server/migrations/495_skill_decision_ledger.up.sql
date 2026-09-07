-- 495: Skill decision/deployment plane (spec §12.4/§12.9, ADR 0021).
--
-- Append-only approvals with a decision-shape floor, deployments whose
-- materialization status is the only mutable field (converged/fenced are
-- terminal), rollbacks that only ever advance their roll-forward status,
-- the single-active-run partial unique index that fences the evolution key
-- at the database level (ADR 0021 D4), and the idempotency/outbox/
-- reconciliation state that makes activation replay-safe and observable.
-- Workspace-scoped composite FKs keep every row inside one tenant; down
-- migrations are for pre-enable environments only (ADR 0021 D8).

-- The D4 fence: one active (non-terminal) run per workspace + evolution
-- key, enforced by the database so even a writer that skips the store's
-- admission check cannot double-claim a lane.
CREATE UNIQUE INDEX skill_evolution_run_single_active_key
    ON skill_evolution_run (workspace_id, evolution_key)
    WHERE status IN ('queued', 'snapshotting', 'consolidating_patterns',
                     'proposing_candidate', 'awaiting_review', 'evaluating',
                     'awaiting_approval');

-- Approval: the human gate. Append-only; the shape CHECK is only a floor
-- (approved records need risk acknowledgement and an expiry), the domain
-- contract plus the store's actor-isolation checks are the authority.
CREATE TABLE skill_approval (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    approval_id TEXT NOT NULL CHECK (approval_id <> '' AND length(approval_id) <= 256),
    candidate_id TEXT NOT NULL,
    evaluation_id TEXT NOT NULL,
    manifest_hash TEXT NOT NULL CONSTRAINT skill_approval_manifest_hash_check
        CHECK (manifest_hash ~ '^sha256:[0-9a-f]{64}$'),
    policy_hash TEXT NOT NULL CONSTRAINT skill_approval_policy_hash_check
        CHECK (policy_hash ~ '^sha256:[0-9a-f]{64}$'),
    artifact_hash TEXT NOT NULL CONSTRAINT skill_approval_artifact_hash_check
        CHECK (artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    target_scope TEXT NOT NULL CHECK (target_scope IN ('agent', 'channel', 'workspace')),
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    approver_actor TEXT NOT NULL CHECK (approver_actor <> ''),
    approver_role TEXT NOT NULL CHECK (approver_role <> ''),
    reason TEXT NOT NULL DEFAULT '',
    risk_acknowledged BOOLEAN NOT NULL,
    allow_auto_rollback BOOLEAN NOT NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, approval_id),
    CONSTRAINT skill_approval_candidate_fk
        FOREIGN KEY (workspace_id, candidate_id)
        REFERENCES skill_candidate (workspace_id, candidate_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_approval_evaluation_fk
        FOREIGN KEY (workspace_id, evaluation_id)
        REFERENCES skill_evaluation_run (workspace_id, evaluation_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_approval_decision_shape_check CHECK (
        (decision = 'approved' AND risk_acknowledged AND expires_at IS NOT NULL)
        OR decision = 'rejected'
    )
);

CREATE INDEX idx_skill_approval_candidate
    ON skill_approval (workspace_id, candidate_id, created_at DESC);

CREATE TRIGGER skill_approval_append_only
    BEFORE UPDATE OR DELETE ON skill_approval
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

-- Deployment/promotion: one activation into a target scope with the
-- binding/grant before/after state. Only materialization_status (and
-- updated_at) is meant to change; converged and fenced are terminal.
CREATE TABLE skill_deployment (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    deployment_id TEXT NOT NULL CHECK (deployment_id <> '' AND length(deployment_id) <= 256),
    candidate_id TEXT NOT NULL,
    approval_id TEXT NOT NULL,
    target_scope TEXT NOT NULL CHECK (target_scope IN ('agent', 'channel', 'workspace')),
    target_agent_id UUID REFERENCES agent(id) ON DELETE CASCADE,
    target_channel_id UUID REFERENCES channel(id) ON DELETE CASCADE,
    binding_state_before TEXT NOT NULL DEFAULT '',
    binding_state_after TEXT NOT NULL DEFAULT '',
    from_artifact_hash TEXT NOT NULL CONSTRAINT skill_deployment_from_hash_check
        CHECK (from_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    to_artifact_hash TEXT NOT NULL CONSTRAINT skill_deployment_to_hash_check
        CHECK (to_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    materialization_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (materialization_status IN ('pending', 'converged', 'failed', 'fenced')),
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, deployment_id),
    CONSTRAINT skill_deployment_candidate_fk
        FOREIGN KEY (workspace_id, candidate_id)
        REFERENCES skill_candidate (workspace_id, candidate_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_deployment_approval_fk
        FOREIGN KEY (workspace_id, approval_id)
        REFERENCES skill_approval (workspace_id, approval_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_deployment_target_shape_check CHECK (
        (target_scope = 'agent' AND target_agent_id IS NOT NULL AND target_channel_id IS NULL)
        OR (target_scope = 'channel' AND target_channel_id IS NOT NULL AND target_agent_id IS NULL)
        OR (target_scope = 'workspace' AND target_agent_id IS NULL AND target_channel_id IS NULL)
    )
);

CREATE INDEX idx_skill_deployment_candidate
    ON skill_deployment (workspace_id, candidate_id, created_at DESC);

CREATE FUNCTION skill_deployment_materialization_guard() RETURNS trigger AS $$
BEGIN
    IF OLD.materialization_status IN ('converged', 'fenced')
       AND NEW.materialization_status <> OLD.materialization_status THEN
        RAISE EXCEPTION 'skill deployment % already %: materialization is terminal',
            OLD.deployment_id, OLD.materialization_status
            USING ERRCODE = 'raise_exception';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_deployment_materialization_guard
    BEFORE UPDATE ON skill_deployment
    FOR EACH ROW EXECUTE FUNCTION skill_deployment_materialization_guard();

-- Rollback: moves the authoritative active-safe pointer; binding history
-- is never deleted. The update guard freezes everything except
-- roll_forward_status; deletes are refused outright.
CREATE TABLE skill_rollback (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    rollback_id TEXT NOT NULL CHECK (rollback_id <> '' AND length(rollback_id) <= 256),
    deployment_id TEXT NOT NULL,
    rollback_trigger TEXT NOT NULL CHECK (rollback_trigger IN (
        'safety_fence', 'performance_regression', 'manual', 'source_retraction'
    )),
    from_artifact_hash TEXT NOT NULL CONSTRAINT skill_rollback_from_hash_check
        CHECK (from_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    to_artifact_hash TEXT NOT NULL CONSTRAINT skill_rollback_to_hash_check
        CHECK (to_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    in_flight_policy TEXT NOT NULL CHECK (in_flight_policy IN ('fenced', 'drain', 'pin')),
    actor TEXT NOT NULL CHECK (actor <> ''),
    policy_version TEXT NOT NULL DEFAULT '',
    roll_forward_status TEXT NOT NULL DEFAULT 'none'
        CHECK (roll_forward_status IN ('none', 'pending', 'opened', 'superseded')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, rollback_id),
    CONSTRAINT skill_rollback_deployment_fk
        FOREIGN KEY (workspace_id, deployment_id)
        REFERENCES skill_deployment (workspace_id, deployment_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_rollback_hash_change_check
        CHECK (from_artifact_hash <> to_artifact_hash)
);

CREATE INDEX idx_skill_rollback_deployment
    ON skill_rollback (workspace_id, deployment_id, created_at DESC);

CREATE FUNCTION skill_rollback_update_guard() RETURNS trigger AS $$
BEGIN
    IF NEW.workspace_id <> OLD.workspace_id OR NEW.rollback_id <> OLD.rollback_id
       OR NEW.deployment_id <> OLD.deployment_id OR NEW.rollback_trigger <> OLD.rollback_trigger
       OR NEW.from_artifact_hash <> OLD.from_artifact_hash
       OR NEW.to_artifact_hash <> OLD.to_artifact_hash
       OR NEW.in_flight_policy <> OLD.in_flight_policy OR NEW.actor <> OLD.actor
       OR NEW.policy_version <> OLD.policy_version OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'skill rollback % is append-only: only roll_forward_status may advance',
            OLD.rollback_id
            USING ERRCODE = 'raise_exception';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_rollback_update_guard
    BEFORE UPDATE ON skill_rollback
    FOR EACH ROW EXECUTE FUNCTION skill_rollback_update_guard();

CREATE TRIGGER skill_rollback_no_delete
    BEFORE DELETE ON skill_rollback
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

-- Idempotency: one key names one payload. A replay with the same payload
-- returns the recorded response; a different payload under the same key
-- is a conflict, never an overwrite.
CREATE TABLE skill_evolution_idempotency (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (idempotency_key <> '' AND length(idempotency_key) <= 256),
    request_kind TEXT NOT NULL CHECK (request_kind <> '' AND length(request_kind) <= 128),
    payload_hash TEXT NOT NULL CONSTRAINT skill_idempotency_payload_hash_check
        CHECK (payload_hash ~ '^sha256:[0-9a-f]{64}$'),
    response JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, idempotency_key)
);

-- Outbox: activation side effects (materialization, rollback signals) are
-- published transactionally and dispatched at least once; reconciliation
-- walks the pending slice by created_at.
CREATE TABLE skill_evolution_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    aggregate_kind TEXT NOT NULL CHECK (aggregate_kind <> '' AND length(aggregate_kind) <= 64),
    aggregate_id TEXT NOT NULL CHECK (aggregate_id <> '' AND length(aggregate_id) <= 256),
    event_type TEXT NOT NULL CHECK (event_type <> '' AND length(event_type) <= 128),
    payload JSONB NOT NULL,
    dispatched_at TIMESTAMPTZ,
    dispatch_attempts INT NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_skill_evolution_outbox_pending
    ON skill_evolution_outbox (created_at)
    WHERE dispatched_at IS NULL;
CREATE INDEX idx_skill_evolution_outbox_aggregate
    ON skill_evolution_outbox (workspace_id, aggregate_kind, aggregate_id);

-- Reconciliation checkpoint per workspace and lane: the last sweep
-- position and the pending backlog it observed.
CREATE TABLE skill_evolution_reconciliation (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    lane TEXT NOT NULL CHECK (lane <> '' AND length(lane) <= 128),
    last_reconciled_at TIMESTAMPTZ NOT NULL,
    pending_count INT NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, lane)
);

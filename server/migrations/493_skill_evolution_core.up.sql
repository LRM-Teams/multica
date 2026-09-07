-- 493: Skill evolution core ledger (spec §12.4, ADR 0021).
--
-- Append-only pattern revisions and candidate artifacts, a stateful
-- (non-append-only) orchestrator run row whose terminal statuses are
-- guarded by trigger, and workspace-scoped composite foreign keys so no
-- ledger row can reference another tenant's run, pattern, candidate, or
-- skill. The evaluation plane (494) and decision/deployment plane (495)
-- build on these tables; the single-active-run partial unique index lands
-- with 495 so pre-495 writers are not fenced by an index they cannot see.

-- Orchestrator run: status transitions follow the Phase 0 state machine in
-- server/internal/skillevolution/types.go. The trigger below is the
-- DB-level floor, not the authority: the store applies CanTransition with
-- a CAS and the trigger only guarantees terminal runs are never revived
-- and timestamps stay monotonic.
CREATE TABLE skill_evolution_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- UNIQUE (workspace_id, id) enables workspace-scoped composite FKs from
    -- candidate rows; the PK alone cannot be referenced scoped.
    CONSTRAINT skill_evolution_run_workspace_id_id_key UNIQUE (workspace_id, id),
    target_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    task_type TEXT NOT NULL CHECK (task_type <> '' AND length(task_type) <= 128),
    environment_major_version TEXT NOT NULL CHECK (environment_major_version <> '' AND length(environment_major_version) <= 128),
    -- The evolution key body (ADR 0021 D4): the workspace scope is the
    -- workspace_id column itself, so the stored key is the agent-scoped
    -- suffix and generated columns keep it consistent with its parts.
    evolution_key TEXT GENERATED ALWAYS AS (
        target_agent_id::text || ':' || task_type || ':' || environment_major_version
    ) STORED,
    status TEXT NOT NULL DEFAULT 'queued',
    pinned_inputs JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    terminal_at TIMESTAMPTZ,
    CONSTRAINT skill_evolution_run_status_check CHECK (status IN (
        'queued', 'snapshotting', 'consolidating_patterns', 'proposing_candidate',
        'awaiting_review', 'evaluating', 'awaiting_approval',
        'completed', 'no_action', 'rejected', 'cancelled', 'failed', 'stale', 'fenced'
    )),
    CONSTRAINT skill_evolution_run_terminal_at_shape_check CHECK (
        (status IN ('completed', 'no_action', 'rejected', 'cancelled', 'failed', 'stale', 'fenced')
         AND terminal_at IS NOT NULL)
        OR (status NOT IN ('completed', 'no_action', 'rejected', 'cancelled', 'failed', 'stale', 'fenced')
            AND terminal_at IS NULL)
    )
);

CREATE INDEX idx_skill_evolution_run_key
    ON skill_evolution_run (workspace_id, evolution_key, created_at DESC);
CREATE INDEX idx_skill_evolution_run_workspace_created
    ON skill_evolution_run (workspace_id, created_at DESC);

CREATE FUNCTION skill_evolution_run_terminal_guard() RETURNS trigger AS $$
BEGIN
    IF OLD.status IN ('completed', 'no_action', 'rejected', 'cancelled', 'failed', 'stale', 'fenced')
       AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'skill evolution run % is terminal (%): it cannot be revived or transitioned', OLD.id, OLD.status
            USING ERRCODE = 'raise_exception';
    END IF;
    IF NEW.status IN ('completed', 'no_action', 'rejected', 'cancelled', 'failed', 'stale', 'fenced')
       AND OLD.status NOT IN ('completed', 'no_action', 'rejected', 'cancelled', 'failed', 'stale', 'fenced') THEN
        NEW.terminal_at := now();
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_evolution_run_terminal_guard
    BEFORE UPDATE ON skill_evolution_run
    FOR EACH ROW EXECUTE FUNCTION skill_evolution_run_terminal_guard();

-- Pattern identity plus INSERT-only revisions (ADR 0021 D2/D6): content
-- never mutates in place; status changes ship as new revisions and the
-- parent row only advances its current-revision pointer.
CREATE TABLE skill_pattern (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    pattern_id TEXT NOT NULL CHECK (pattern_id <> '' AND length(pattern_id) <= 256),
    evolution_key TEXT NOT NULL CHECK (evolution_key <> ''),
    task_type TEXT NOT NULL DEFAULT '',
    current_revision BIGINT NOT NULL DEFAULT 0 CHECK (current_revision >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, pattern_id)
);

CREATE INDEX idx_skill_pattern_workspace_updated
    ON skill_pattern (workspace_id, updated_at DESC);

CREATE TABLE skill_pattern_revision (
    workspace_id UUID NOT NULL,
    pattern_id TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision >= 1),
    pattern_kind TEXT NOT NULL CHECK (pattern_kind IN ('success', 'failure', 'mixed')),
    status TEXT NOT NULL CHECK (status IN (
        'tentative', 'supported', 'contradicted', 'refuted', 'stale'
    )),
    problem TEXT NOT NULL CHECK (problem <> ''),
    applicability TEXT NOT NULL CHECK (applicability <> ''),
    root_cause_summary TEXT NOT NULL CHECK (root_cause_summary <> ''),
    recommended_action TEXT NOT NULL CHECK (recommended_action <> ''),
    task_type TEXT NOT NULL DEFAULT '',
    source_model_id TEXT NOT NULL DEFAULT '',
    target_model_id TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    tool_capability_id TEXT NOT NULL DEFAULT '',
    runtime_id TEXT NOT NULL DEFAULT '',
    environment_key TEXT NOT NULL DEFAULT '',
    generator_version TEXT NOT NULL DEFAULT '',
    policy_version TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL CONSTRAINT skill_pattern_revision_hash_check
        CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, pattern_id, revision),
    CONSTRAINT skill_pattern_revision_pattern_fk
        FOREIGN KEY (workspace_id, pattern_id)
        REFERENCES skill_pattern (workspace_id, pattern_id)
        ON DELETE CASCADE
);

CREATE INDEX idx_skill_pattern_revision_latest
    ON skill_pattern_revision (workspace_id, pattern_id, revision DESC);

-- Append-only: ledger revisions are immutable; corrections are new
-- revisions. Down migrations drop the table wholesale for pre-enable
-- environments only (ADR 0021 D8).
CREATE FUNCTION skill_pattern_revision_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'skill_pattern_revision is append-only (workspace % pattern % revision %)', 
        COALESCE(NEW.workspace_id::text, OLD.workspace_id::text),
        COALESCE(NEW.pattern_id, OLD.pattern_id),
        COALESCE(NEW.revision, OLD.revision)
            USING ERRCODE = 'raise_exception';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_pattern_revision_append_only
    BEFORE UPDATE OR DELETE ON skill_pattern_revision
    FOR EACH ROW EXECUTE FUNCTION skill_pattern_revision_append_only();

-- Evidence refs of one revision, normalized from the PatternRecord's
-- positive/negative SkillEvolutionRef arrays. Same-workspace only: the
-- milestone explicitly does not aggregate patterns across workspaces.
CREATE TABLE skill_pattern_evidence (
    workspace_id UUID NOT NULL,
    pattern_id TEXT NOT NULL,
    revision BIGINT NOT NULL,
    polarity TEXT NOT NULL CHECK (polarity IN ('positive', 'negative')),
    ref_kind TEXT NOT NULL CHECK (ref_kind IN (
        'pattern', 'skill_candidate', 'assertion_manifest', 'evaluation_run', 'approval'
    )),
    ref_id TEXT NOT NULL CHECK (ref_id <> '' AND length(ref_id) <= 256),
    ref_workspace_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, pattern_id, revision, polarity, ref_kind, ref_id),
    CONSTRAINT skill_pattern_evidence_revision_fk
        FOREIGN KEY (workspace_id, pattern_id, revision)
        REFERENCES skill_pattern_revision (workspace_id, pattern_id, revision)
        ON DELETE CASCADE,
    CONSTRAINT skill_pattern_evidence_same_workspace_check
        CHECK (ref_workspace_id = workspace_id::text)
);

CREATE TRIGGER skill_pattern_evidence_append_only
    BEFORE UPDATE OR DELETE ON skill_pattern_evidence
    FOR EACH ROW EXECUTE FUNCTION skill_pattern_revision_append_only();

-- The skill_candidate composite FK below needs a unique (workspace_id, id)
-- on skill; the PK alone cannot be referenced scoped.
CREATE UNIQUE INDEX skill_workspace_id_id_key ON skill (workspace_id, id);

-- SkillCandidate: the contract-validated proposal. The immutable contract
-- payload is pinned by contract_hash; status transitions follow the Phase 0
-- machine with terminal statuses guarded by trigger.
CREATE TABLE skill_candidate (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    candidate_id TEXT NOT NULL CHECK (candidate_id <> '' AND length(candidate_id) <= 256),
    run_id UUID NOT NULL,
    target_skill_id UUID,
    new_skill_name TEXT NOT NULL DEFAULT '',
    requested_scope TEXT NOT NULL CHECK (requested_scope IN ('agent', 'channel', 'workspace')),
    base_artifact_hash TEXT NOT NULL CONSTRAINT skill_candidate_base_hash_check
        CHECK (base_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    candidate_artifact_hash TEXT NOT NULL CONSTRAINT skill_candidate_artifact_hash_check
        CHECK (candidate_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    proposed_diff_hash TEXT NOT NULL CONSTRAINT skill_candidate_diff_hash_check
        CHECK (proposed_diff_hash ~ '^sha256:[0-9a-f]{64}$'),
    contract_hash TEXT NOT NULL CONSTRAINT skill_candidate_contract_hash_check
        CHECK (contract_hash ~ '^sha256:[0-9a-f]{64}$'),
    contract JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'needs_review' CHECK (status IN (
        'needs_review', 'shadow', 'evaluating', 'accepted',
        'rejected', 'stale', 'withdrawn', 'superseded'
    )),
    current_artifact_version INT NOT NULL DEFAULT 1 CHECK (current_artifact_version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, candidate_id),
    CONSTRAINT skill_candidate_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES skill_evolution_run (workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT skill_candidate_target_skill_fk
        FOREIGN KEY (workspace_id, target_skill_id)
        REFERENCES skill (workspace_id, id)
        ON DELETE SET NULL,
    CONSTRAINT skill_candidate_shape_check CHECK (
        (target_skill_id IS NOT NULL AND new_skill_name = '')
        OR (target_skill_id IS NULL AND new_skill_name <> '')
    )
);

CREATE INDEX idx_skill_candidate_workspace_created
    ON skill_candidate (workspace_id, created_at DESC);
CREATE INDEX idx_skill_candidate_run
    ON skill_candidate (workspace_id, run_id);

CREATE FUNCTION skill_candidate_terminal_guard() RETURNS trigger AS $$
BEGIN
    IF OLD.status IN ('rejected', 'stale', 'withdrawn', 'superseded')
       AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'skill candidate % is terminal (%): a materially changed proposal is a new candidate', OLD.candidate_id, OLD.status
            USING ERRCODE = 'raise_exception';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_candidate_terminal_guard
    BEFORE UPDATE ON skill_candidate
    FOR EACH ROW EXECUTE FUNCTION skill_candidate_terminal_guard();

-- Candidate artifacts: INSERT-only rows under the candidate, copying the
-- shared_evolution_unit_version shape (UNIQUE per candidate+version); the
-- candidate's current_artifact_version pointer advances by CAS in the
-- store (ADR 0021 D2).
CREATE TABLE skill_candidate_artifact (
    workspace_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    version INT NOT NULL CHECK (version >= 1),
    artifact_hash TEXT NOT NULL CONSTRAINT skill_candidate_artifact_row_hash_check
        CHECK (artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    storage_ref TEXT NOT NULL DEFAULT '' CHECK (length(storage_ref) <= 1024),
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, candidate_id, version),
    CONSTRAINT skill_candidate_artifact_candidate_fk
        FOREIGN KEY (workspace_id, candidate_id)
        REFERENCES skill_candidate (workspace_id, candidate_id)
        ON DELETE CASCADE
);

CREATE TRIGGER skill_candidate_artifact_append_only
    BEFORE UPDATE OR DELETE ON skill_candidate_artifact
    FOR EACH ROW EXECUTE FUNCTION skill_pattern_revision_append_only();

-- Motivating patterns of one candidate, workspace-scoped on both sides.
CREATE TABLE skill_candidate_pattern (
    workspace_id UUID NOT NULL,
    candidate_id TEXT NOT NULL,
    pattern_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, candidate_id, pattern_id),
    CONSTRAINT skill_candidate_pattern_candidate_fk
        FOREIGN KEY (workspace_id, candidate_id)
        REFERENCES skill_candidate (workspace_id, candidate_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_candidate_pattern_pattern_fk
        FOREIGN KEY (workspace_id, pattern_id)
        REFERENCES skill_pattern (workspace_id, pattern_id)
        ON DELETE CASCADE
);

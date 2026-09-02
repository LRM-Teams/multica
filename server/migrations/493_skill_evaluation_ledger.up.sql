-- 493: Skill evaluation plane (spec §12.4, ADR 0021).
--
-- Immutable, versioned assertion manifests; append-only evaluation runs
-- with per-assertion results; dataset/environment identity and
-- contamination status on every run. Workspace-scoped composite FKs keep
-- every evaluation row inside one tenant, and result rows may only
-- reference assertions the pinned manifest version declares (the result FK
-- targets skill_assertion, so membership integrity is enforced by the
-- database, not just the store). Down migrations drop these tables for
-- pre-enable environments only (ADR 0021 D8).

CREATE TABLE skill_assertion_manifest (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    manifest_id TEXT NOT NULL CHECK (manifest_id <> '' AND length(manifest_id) <= 256),
    version INT NOT NULL CHECK (version >= 1),
    manifest_hash TEXT NOT NULL CONSTRAINT skill_assertion_manifest_hash_check
        CHECK (manifest_hash ~ '^sha256:[0-9a-f]{64}$'),
    dataset_identity TEXT NOT NULL CHECK (dataset_identity <> '' AND length(dataset_identity) <= 256),
    dataset_version TEXT NOT NULL DEFAULT '' CHECK (length(dataset_version) <= 128),
    lineage_split TEXT NOT NULL DEFAULT '' CHECK (length(lineage_split) <= 256),
    domain_profile TEXT NOT NULL DEFAULT '' CHECK (length(domain_profile) <= 128),
    task_slices JSONB NOT NULL DEFAULT '[]'::jsonb,
    evaluator_version TEXT NOT NULL DEFAULT '' CHECK (length(evaluator_version) <= 128),
    scorer_version TEXT NOT NULL DEFAULT '' CHECK (length(scorer_version) <= 128),
    environment_key TEXT NOT NULL DEFAULT '' CHECK (length(environment_key) <= 256),
    required_capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    data_residency TEXT NOT NULL DEFAULT '' CHECK (length(data_residency) <= 128),
    contract JSONB NOT NULL,
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, manifest_id, version)
);

CREATE INDEX idx_skill_assertion_manifest_workspace_created
    ON skill_assertion_manifest (workspace_id, created_at DESC);

-- Assertions declared by one manifest version. INSERT-only: a changed
-- assertion set is a new manifest version, never an edit.
CREATE TABLE skill_assertion (
    workspace_id UUID NOT NULL,
    manifest_id TEXT NOT NULL,
    manifest_version INT NOT NULL,
    assertion_id TEXT NOT NULL CHECK (assertion_id <> '' AND length(assertion_id) <= 256),
    kind TEXT NOT NULL CHECK (kind <> '' AND length(kind) <= 128),
    oracle_ref_hash TEXT NOT NULL CONSTRAINT skill_assertion_oracle_hash_check
        CHECK (oracle_ref_hash ~ '^sha256:[0-9a-f]{64}$'),
    severity TEXT NOT NULL CHECK (severity <> '' AND length(severity) <= 64),
    hard BOOLEAN NOT NULL,
    required BOOLEAN NOT NULL,
    tolerance TEXT NOT NULL DEFAULT '' CHECK (length(tolerance) <= 256),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, manifest_id, manifest_version, assertion_id),
    CONSTRAINT skill_assertion_manifest_fk
        FOREIGN KEY (workspace_id, manifest_id, manifest_version)
        REFERENCES skill_assertion_manifest (workspace_id, manifest_id, version)
        ON DELETE CASCADE
);

-- Generic append-only guard for the evaluation plane. Reports the actual
-- table name so the failure is attributable.
CREATE FUNCTION skill_ledger_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only (skill evaluation plane): rows are immutable', TG_TABLE_NAME
        USING ERRCODE = 'raise_exception';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_assertion_manifest_append_only
    BEFORE UPDATE OR DELETE ON skill_assertion_manifest
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

CREATE TRIGGER skill_assertion_append_only
    BEFORE UPDATE OR DELETE ON skill_assertion
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

-- EvaluationRun: one append-only scoring pass. Retries and changed
-- scorer/policy/manifest land as new runs, never overwrites. The
-- contamination gate CHECK mirrors the domain rule (a contaminated run
-- cannot pass) so a store bug cannot bypass the contract.
CREATE TABLE skill_evaluation_run (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    evaluation_id TEXT NOT NULL CHECK (evaluation_id <> '' AND length(evaluation_id) <= 256),
    candidate_id TEXT NOT NULL CHECK (candidate_id <> '' AND length(candidate_id) <= 256),
    manifest_id TEXT NOT NULL,
    manifest_version INT NOT NULL,
    base_artifact_hash TEXT NOT NULL CONSTRAINT skill_evaluation_run_base_hash_check
        CHECK (base_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    candidate_artifact_hash TEXT NOT NULL CONSTRAINT skill_evaluation_run_candidate_hash_check
        CHECK (candidate_artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
    manifest_hash TEXT NOT NULL CONSTRAINT skill_evaluation_run_manifest_hash_check
        CHECK (manifest_hash ~ '^sha256:[0-9a-f]{64}$'),
    target_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    target_model_id TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    tool_capability_id TEXT NOT NULL DEFAULT '',
    runtime_id TEXT NOT NULL DEFAULT '',
    environment_key TEXT NOT NULL DEFAULT '',
    metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    contamination_status TEXT NOT NULL CHECK (contamination_status IN ('clean', 'suspected', 'confirmed')),
    decision_policy_version TEXT NOT NULL DEFAULT '',
    terminal_result TEXT NOT NULL CHECK (terminal_result IN (
        'passed', 'failed', 'inconclusive', 'infrastructure_invalid'
    )),
    terminal_reason TEXT NOT NULL DEFAULT '',
    created_by_actor TEXT NOT NULL CHECK (created_by_actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, evaluation_id),
    CONSTRAINT skill_evaluation_run_candidate_fk
        FOREIGN KEY (workspace_id, candidate_id)
        REFERENCES skill_candidate (workspace_id, candidate_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_evaluation_run_manifest_fk
        FOREIGN KEY (workspace_id, manifest_id, manifest_version)
        REFERENCES skill_assertion_manifest (workspace_id, manifest_id, version),
    CONSTRAINT skill_evaluation_run_contamination_gate_check
        CHECK (NOT (contamination_status = 'confirmed' AND terminal_result = 'passed'))
);

CREATE INDEX idx_skill_evaluation_run_candidate
    ON skill_evaluation_run (workspace_id, candidate_id, created_at DESC);
CREATE INDEX idx_skill_evaluation_run_workspace_created
    ON skill_evaluation_run (workspace_id, created_at DESC);

CREATE TRIGGER skill_evaluation_run_append_only
    BEFORE UPDATE OR DELETE ON skill_evaluation_run
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

-- Per-assertion outcomes of one run. The manifest columns are denormalized
-- from the run row; the store writes both from the same record inside one
-- transaction, and the scoped FK below only accepts assertions declared by
-- that exact manifest version.
CREATE TABLE skill_evaluation_assertion_result (
    workspace_id UUID NOT NULL,
    evaluation_id TEXT NOT NULL,
    manifest_id TEXT NOT NULL,
    manifest_version INT NOT NULL,
    assertion_id TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('pass', 'fail', 'error', 'not_run')),
    evidence_hash TEXT NOT NULL CONSTRAINT skill_evaluation_result_evidence_hash_check
        CHECK (evidence_hash ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, evaluation_id, assertion_id),
    CONSTRAINT skill_evaluation_result_run_fk
        FOREIGN KEY (workspace_id, evaluation_id)
        REFERENCES skill_evaluation_run (workspace_id, evaluation_id)
        ON DELETE CASCADE,
    CONSTRAINT skill_evaluation_result_assertion_fk
        FOREIGN KEY (workspace_id, manifest_id, manifest_version, assertion_id)
        REFERENCES skill_assertion (workspace_id, manifest_id, manifest_version, assertion_id)
        ON DELETE CASCADE
);

CREATE TRIGGER skill_evaluation_assertion_result_append_only
    BEFORE UPDATE OR DELETE ON skill_evaluation_assertion_result
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

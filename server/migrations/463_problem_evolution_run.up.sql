CREATE TABLE problem_evolution_evaluator_contract (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('solution', 'task_harness_reward_only')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'validating', 'frozen', 'invalid')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    contract JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash TEXT NOT NULL DEFAULT '',
    feedback_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    frozen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX problem_evolution_evaluator_contract_workspace_idx
    ON problem_evolution_evaluator_contract(workspace_id, created_at DESC);

CREATE TABLE problem_evolution_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    mode TEXT NOT NULL CHECK (mode IN ('solution', 'task_harness_reward_only')),
    title TEXT NOT NULL DEFAULT '',
    problem_spec JSONB NOT NULL DEFAULT '{}'::jsonb,
    artifact_type TEXT NOT NULL DEFAULT 'markdown',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN (
        'draft', 'validating_evaluator', 'ready', 'queued', 'running',
        'synthesizing', 'stopping', 'completed', 'cancelled', 'failed'
    )),
    stage TEXT NOT NULL DEFAULT '',
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    model_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    budget_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    stop_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    evaluator_contract_id UUID REFERENCES problem_evolution_evaluator_contract(id) ON DELETE RESTRICT,
    -- Pinned at start so a later contract edit cannot silently change scoring
    -- mid-run; every evaluation re-checks this against the contract row.
    evaluator_content_hash TEXT NOT NULL DEFAULT '',
    evolver_version TEXT NOT NULL DEFAULT '',
    best_candidate_id UUID,
    final_candidate_id UUID,
    graph_version BIGINT NOT NULL DEFAULT 0,
    claimed_runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    claim_token UUID,
    claimed_at TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    stop_requested_at TIMESTAMPTZ,
    stop_reason TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX problem_evolution_run_workspace_idx
    ON problem_evolution_run(workspace_id, created_at DESC);
CREATE INDEX problem_evolution_run_claimable_idx
    ON problem_evolution_run(status, created_at)
    WHERE status = 'queued';
CREATE INDEX problem_evolution_run_active_idx
    ON problem_evolution_run(claimed_runtime_id, heartbeat_at)
    WHERE status IN ('running', 'synthesizing', 'stopping');

CREATE TABLE problem_evolution_candidate (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- Identifier minted by the external evolver; unique per run so repeated
    -- event delivery maps back to the same candidate row.
    external_ref TEXT NOT NULL,
    generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0),
    lane TEXT NOT NULL DEFAULT 'baseline',
    operator TEXT NOT NULL DEFAULT 'baseline',
    status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN (
        'planned', 'producing', 'validating', 'evaluating', 'selectable',
        'elite', 'selected', 'pruned', 'failed', 'timeout', 'infeasible'
    )),
    score JSONB,
    behavior_profile JSONB,
    feasible BOOLEAN NOT NULL DEFAULT true,
    artifact_ref TEXT NOT NULL DEFAULT '',
    artifact_hash TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    change_summary TEXT NOT NULL DEFAULT '',
    failure_class TEXT NOT NULL DEFAULT '',
    runtime_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    token_usage JSONB NOT NULL DEFAULT '{}'::jsonb,
    cost NUMERIC(12, 6) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, external_ref)
);

CREATE INDEX problem_evolution_candidate_run_idx
    ON problem_evolution_candidate(run_id, generation, created_at);

CREATE TABLE problem_evolution_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    -- Idempotency key minted by the external evolver / daemon. Retried
    -- delivery of the same event must not allocate a second seq.
    client_event_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    candidate_id UUID REFERENCES problem_evolution_candidate(id) ON DELETE SET NULL,
    actor_type TEXT NOT NULL DEFAULT 'daemon',
    actor_id TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, seq),
    UNIQUE (run_id, client_event_id)
);

CREATE INDEX problem_evolution_event_replay_idx
    ON problem_evolution_event(run_id, seq);

ALTER TABLE problem_evolution_run
    ADD CONSTRAINT problem_evolution_run_best_candidate_fkey
    FOREIGN KEY (best_candidate_id) REFERENCES problem_evolution_candidate(id) ON DELETE SET NULL;

ALTER TABLE problem_evolution_run
    ADD CONSTRAINT problem_evolution_run_final_candidate_fkey
    FOREIGN KEY (final_candidate_id) REFERENCES problem_evolution_candidate(id) ON DELETE SET NULL;

ALTER TABLE problem_evolution_run
    DROP CONSTRAINT IF EXISTS problem_evolution_run_mode_check;
ALTER TABLE problem_evolution_run
    ADD CONSTRAINT problem_evolution_run_mode_check
    CHECK (mode IN ('solution', 'task_harness_reward_only', 'task_harness_persistent'));

ALTER TABLE problem_evolution_evaluator_contract
    DROP CONSTRAINT IF EXISTS problem_evolution_evaluator_contract_mode_check;
ALTER TABLE problem_evolution_evaluator_contract
    ADD CONSTRAINT problem_evolution_evaluator_contract_mode_check
    CHECK (mode IN ('solution', 'task_harness_reward_only', 'task_harness_persistent'));

CREATE TABLE problem_evolution_task_set (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source TEXT NOT NULL DEFAULT '',
    dataset_ref TEXT NOT NULL,
    dataset_revision TEXT NOT NULL DEFAULT '',
    task_names JSONB NOT NULL DEFAULT '[]'::jsonb,
    holdout_task_names JSONB NOT NULL DEFAULT '[]'::jsonb,
    rollouts_per_task INTEGER NOT NULL DEFAULT 1 CHECK (rollouts_per_task > 0),
    max_parallel INTEGER NOT NULL DEFAULT 4 CHECK (max_parallel > 0),
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(task_names) = 'array'),
    CHECK (jsonb_typeof(holdout_task_names) = 'array')
);
CREATE INDEX problem_evolution_task_set_workspace_idx
    ON problem_evolution_task_set(workspace_id, created_at DESC);

ALTER TABLE problem_evolution_run
    ADD COLUMN task_set_id UUID REFERENCES problem_evolution_task_set(id) ON DELETE SET NULL;

CREATE TABLE problem_evolution_harness_version (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    iteration INTEGER NOT NULL CHECK (iteration >= 0),
    parent_version_id UUID REFERENCES problem_evolution_harness_version(id) ON DELETE SET NULL,
    components JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash TEXT NOT NULL,
    rolled_back BOOLEAN NOT NULL DEFAULT false,
    promoted_scope TEXT NOT NULL DEFAULT 'run'
        CHECK (promoted_scope IN ('run', 'workspace')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, content_hash)
);
CREATE INDEX problem_evolution_harness_version_run_idx
    ON problem_evolution_harness_version(run_id, iteration, created_at);

CREATE TABLE problem_evolution_iteration (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    iteration INTEGER NOT NULL CHECK (iteration >= 0),
    input_version_id UUID REFERENCES problem_evolution_harness_version(id) ON DELETE SET NULL,
    evolve_version_id UUID REFERENCES problem_evolution_harness_version(id) ON DELETE SET NULL,
    stage TEXT NOT NULL DEFAULT 'evaluating'
        CHECK (stage IN ('evaluating', 'analyzing', 'improving', 'settled')),
    pass_rate DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (pass_rate BETWEEN 0 AND 1),
    holdout_pass_rate DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (holdout_pass_rate BETWEEN 0 AND 1),
    cost NUMERIC(12, 6) NOT NULL DEFAULT 0 CHECK (cost >= 0),
    tokens BIGINT NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, iteration)
);

CREATE TABLE problem_evolution_task_result (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    iteration_id UUID NOT NULL REFERENCES problem_evolution_iteration(id) ON DELETE CASCADE,
    task_name TEXT NOT NULL,
    rollout_index INTEGER NOT NULL CHECK (rollout_index >= 0),
    split TEXT NOT NULL CHECK (split IN ('search', 'holdout')),
    reward DOUBLE PRECISION NOT NULL CHECK (reward BETWEEN 0 AND 1),
    verdict TEXT NOT NULL CHECK (verdict IN ('pass', 'fail', 'error', 'timeout')),
    trace_ref TEXT NOT NULL DEFAULT '',
    trace_digest_ref TEXT NOT NULL DEFAULT '',
    tokens BIGINT NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    cost NUMERIC(12, 6) NOT NULL DEFAULT 0 CHECK (cost >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (iteration_id, task_name, rollout_index)
);
CREATE INDEX problem_evolution_task_result_run_idx
    ON problem_evolution_task_result(run_id, iteration_id, split, task_name);

CREATE TABLE problem_evolution_change_record (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    iteration_id UUID NOT NULL REFERENCES problem_evolution_iteration(id) ON DELETE CASCADE,
    harness_version_id UUID REFERENCES problem_evolution_harness_version(id) ON DELETE SET NULL,
    component TEXT NOT NULL,
    failure_evidence_ref TEXT NOT NULL DEFAULT '',
    root_cause TEXT NOT NULL,
    fix_summary TEXT NOT NULL,
    predicted_pass_task_names JSONB NOT NULL DEFAULT '[]'::jsonb,
    predicted_risk_task_names JSONB NOT NULL DEFAULT '[]'::jsonb,
    observed_flips JSONB NOT NULL DEFAULT '[]'::jsonb,
    verdict TEXT NOT NULL DEFAULT 'pending'
        CHECK (verdict IN ('pending', 'confirmed', 'refuted', 'inconclusive')),
    action TEXT NOT NULL DEFAULT 'pending'
        CHECK (action IN ('pending', 'kept', 'reverted', 'revised')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX problem_evolution_change_record_run_idx
    ON problem_evolution_change_record(run_id, iteration_id, created_at);

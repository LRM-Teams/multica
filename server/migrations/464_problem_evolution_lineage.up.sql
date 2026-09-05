CREATE TABLE problem_evolution_candidate_edge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    parent_id UUID NOT NULL REFERENCES problem_evolution_candidate(id) ON DELETE CASCADE,
    child_id UUID NOT NULL REFERENCES problem_evolution_candidate(id) ON DELETE CASCADE,
    relation TEXT NOT NULL CHECK (relation IN (
        'derived_from', 'repair_of', 'challenge_of', 'crossover_of', 'synthesis_of'
    )),
    -- Position of this parent for multi-parent operators (crossover), so the
    -- graph can render a stable left/right ordering across refetches.
    parent_index INTEGER NOT NULL DEFAULT 0 CHECK (parent_index >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (child_id, parent_id, relation),
    CHECK (parent_id <> child_id)
);

CREATE INDEX problem_evolution_candidate_edge_run_idx
    ON problem_evolution_candidate_edge(run_id, created_at);
CREATE INDEX problem_evolution_candidate_edge_child_idx
    ON problem_evolution_candidate_edge(child_id, parent_index);

CREATE TABLE problem_evolution_evaluation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL REFERENCES problem_evolution_candidate(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- Which frozen contract produced this row. Kept alongside the hash so an
    -- evaluation stays interpretable even if the contract row is edited later.
    evaluator_contract_id UUID REFERENCES problem_evolution_evaluator_contract(id) ON DELETE SET NULL,
    evaluator_content_hash TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
    phase TEXT NOT NULL DEFAULT 'search' CHECK (phase IN ('search', 'blind_validation')),
    verdict TEXT NOT NULL DEFAULT 'scored' CHECK (verdict IN (
        'scored', 'hard_gate_failed', 'infeasible', 'error', 'timeout'
    )),
    score JSONB,
    behavior_profile JSONB,
    -- What the evolver was allowed to see. Stored separately from `score` so an
    -- audit can prove the hidden answer never crossed the feedback boundary.
    feedback_projection JSONB NOT NULL DEFAULT '{}'::jsonb,
    runtime_seconds DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (candidate_id, phase, attempt)
);

CREATE INDEX problem_evolution_evaluation_run_idx
    ON problem_evolution_evaluation(run_id, created_at);

CREATE TABLE problem_evolution_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    candidate_id UUID REFERENCES problem_evolution_candidate(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'answer' CHECK (kind IN (
        'answer', 'harness', 'patch', 'log', 'report'
    )),
    -- Relative path under the run's artifact directory; validated against
    -- traversal before it is ever persisted.
    storage_ref TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'text/markdown',
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, storage_ref)
);

CREATE INDEX problem_evolution_artifact_candidate_idx
    ON problem_evolution_artifact(candidate_id, kind);

ALTER TABLE problem_evolution_run
    ADD COLUMN generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0),
    ADD COLUMN candidate_count INTEGER NOT NULL DEFAULT 0 CHECK (candidate_count >= 0),
    ADD COLUMN rounds_without_gain INTEGER NOT NULL DEFAULT 0 CHECK (rounds_without_gain >= 0),
    ADD COLUMN best_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN total_cost NUMERIC(12, 6) NOT NULL DEFAULT 0;

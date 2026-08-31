CREATE TABLE problem_evolution_harness (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    candidate_id UUID NOT NULL REFERENCES problem_evolution_candidate(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    harness_ref TEXT NOT NULL,
    -- A winning harness belongs to the run that produced it. There is no
    -- 'workspace' scope on purpose: promoting it would be a claim of general
    -- capability gain that a single run's evidence cannot support.
    scope TEXT NOT NULL DEFAULT 'run' CHECK (scope = 'run'),
    spec JSONB NOT NULL DEFAULT '{}'::jsonb,
    static_gate JSONB NOT NULL DEFAULT '{}'::jsonb,
    gate_passed BOOLEAN NOT NULL DEFAULT false,
    shortlisted BOOLEAN NOT NULL DEFAULT false,
    executed BOOLEAN NOT NULL DEFAULT false,
    winner BOOLEAN NOT NULL DEFAULT false,
    prior_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    reward DOUBLE PRECISION,
    repair_rounds INTEGER NOT NULL DEFAULT 0 CHECK (repair_rounds >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, harness_ref)
);

CREATE INDEX problem_evolution_harness_run_idx
    ON problem_evolution_harness(run_id, created_at);
-- At most one winner per run, enforced by the schema rather than by convention.
CREATE UNIQUE INDEX problem_evolution_harness_winner_idx
    ON problem_evolution_harness(run_id)
    WHERE winner;

ALTER TABLE problem_evolution_run
    ADD COLUMN harness_proposals INTEGER NOT NULL DEFAULT 4
        CHECK (harness_proposals > 0 AND harness_proposals <= 8),
    ADD COLUMN harness_execute_count INTEGER NOT NULL DEFAULT 2
        CHECK (harness_execute_count > 0),
    -- Benchmark mode reproduces the published JIT procedure: generate several,
    -- execute one, no low-reward repair.
    ADD COLUMN benchmark_mode BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN winner_harness_id UUID REFERENCES problem_evolution_harness(id) ON DELETE SET NULL;

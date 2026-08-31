ALTER TABLE problem_evolution_run
    -- Search feedback and the final blind check must not share a seed: reusing
    -- it would let a run that adapted to its search sample score well without
    -- having generalised.
    ADD COLUMN search_seed BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN blind_seed BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN blind_candidate_id UUID REFERENCES problem_evolution_candidate(id) ON DELETE SET NULL,
    ADD COLUMN blind_score DOUBLE PRECISION,
    ADD COLUMN overfit_gap DOUBLE PRECISION;

ALTER TABLE problem_evolution_candidate
    -- Reward-only feedback rounds already spent on this candidate. Derived from
    -- evaluations, cached here so ingestion can refuse an over-budget repair
    -- without a join.
    ADD COLUMN feedback_rounds INTEGER NOT NULL DEFAULT 0 CHECK (feedback_rounds >= 0);

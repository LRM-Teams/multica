ALTER TABLE problem_evolution_candidate
    DROP COLUMN IF EXISTS feedback_rounds;

ALTER TABLE problem_evolution_run
    DROP COLUMN IF EXISTS overfit_gap,
    DROP COLUMN IF EXISTS blind_score,
    DROP COLUMN IF EXISTS blind_candidate_id,
    DROP COLUMN IF EXISTS blind_seed,
    DROP COLUMN IF EXISTS search_seed;

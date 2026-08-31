ALTER TABLE problem_evolution_run
    DROP COLUMN IF EXISTS total_cost,
    DROP COLUMN IF EXISTS best_score,
    DROP COLUMN IF EXISTS rounds_without_gain,
    DROP COLUMN IF EXISTS candidate_count,
    DROP COLUMN IF EXISTS generation;

DROP TABLE IF EXISTS problem_evolution_artifact;
DROP TABLE IF EXISTS problem_evolution_evaluation;
DROP TABLE IF EXISTS problem_evolution_candidate_edge;

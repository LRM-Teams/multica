ALTER TABLE problem_evolution_run
    DROP COLUMN IF EXISTS winner_harness_id,
    DROP COLUMN IF EXISTS benchmark_mode,
    DROP COLUMN IF EXISTS harness_execute_count,
    DROP COLUMN IF EXISTS harness_proposals;

DROP TABLE IF EXISTS problem_evolution_harness;

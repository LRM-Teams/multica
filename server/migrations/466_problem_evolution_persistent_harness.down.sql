DROP TABLE problem_evolution_change_record;
DROP TABLE problem_evolution_task_result;
DROP TABLE problem_evolution_iteration;
DROP TABLE problem_evolution_harness_version;
ALTER TABLE problem_evolution_run DROP COLUMN task_set_id;
DROP TABLE problem_evolution_task_set;

ALTER TABLE problem_evolution_run
    DROP CONSTRAINT IF EXISTS problem_evolution_run_mode_check;
ALTER TABLE problem_evolution_run
    ADD CONSTRAINT problem_evolution_run_mode_check
    CHECK (mode IN ('solution', 'task_harness_reward_only'));

ALTER TABLE problem_evolution_evaluator_contract
    DROP CONSTRAINT IF EXISTS problem_evolution_evaluator_contract_mode_check;
ALTER TABLE problem_evolution_evaluator_contract
    ADD CONSTRAINT problem_evolution_evaluator_contract_mode_check
    CHECK (mode IN ('solution', 'task_harness_reward_only'));

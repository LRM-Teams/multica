-- 208_interaction_dag_diagnosis_state down: drop the diagnosis run/segment
-- checkpoint tables and their index. interaction_dag_step_reward is untouched.

DROP INDEX IF EXISTS idx_interaction_dag_diagnosis_run_resumable;
DROP TABLE IF EXISTS interaction_dag_diagnosis_segment;
DROP TABLE IF EXISTS interaction_dag_diagnosis_run;

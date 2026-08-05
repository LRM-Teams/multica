ALTER TABLE interaction_dag_diagnosis_run
  DROP CONSTRAINT interaction_dag_diagnosis_run_sandbox_mode_check;

ALTER TABLE interaction_dag_diagnosis_run
  DROP COLUMN sandbox_mode;

ALTER TABLE interaction_dag_diagnosis_run
  DROP CONSTRAINT interaction_dag_diagnosis_run_status_check;
ALTER TABLE interaction_dag_diagnosis_run
  ADD CONSTRAINT interaction_dag_diagnosis_run_status_check
    CHECK (status IN ('running', 'compacting', 'completed', 'failed'));

ALTER TABLE interaction_dag_diagnosis_run
  DROP COLUMN sandbox_instance_id,
  DROP COLUMN capability_token_hash,
  DROP COLUMN execution_mode;

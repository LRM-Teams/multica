-- 278_diagnosis_run_sandbox: link a diagnosis run to its dedicated sandbox
-- and per-run capability token (spec 005 diagnosis-agent-sandbox), and add
-- the 'provisioning' run status used while a sandbox-mode run waits for its
-- sandbox and runtime to come online.

ALTER TABLE interaction_dag_diagnosis_run
  ADD COLUMN sandbox_instance_id text,
  ADD COLUMN capability_token_hash text,
  ADD COLUMN execution_mode text;

ALTER TABLE interaction_dag_diagnosis_run
  DROP CONSTRAINT interaction_dag_diagnosis_run_status_check;
ALTER TABLE interaction_dag_diagnosis_run
  ADD CONSTRAINT interaction_dag_diagnosis_run_status_check
    CHECK (status IN ('provisioning', 'running', 'compacting', 'completed', 'failed'));

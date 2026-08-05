-- 283_diagnosis_run_sandbox_mode: record whether sandbox-mode diagnosis
-- owns a dedicated sandbox or borrows the dispatch team's shared sandbox.

ALTER TABLE interaction_dag_diagnosis_run
  ADD COLUMN sandbox_mode text;

ALTER TABLE interaction_dag_diagnosis_run
  ADD CONSTRAINT interaction_dag_diagnosis_run_sandbox_mode_check
  CHECK (sandbox_mode IS NULL OR sandbox_mode IN ('dedicated', 'shared'));

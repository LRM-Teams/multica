-- At most one active diagnosis run may exist for a business dispatch. Terminal
-- runs remain historical and allow an explicit later retrigger.
CREATE UNIQUE INDEX interaction_dag_diagnosis_run_active_unique
  ON interaction_dag_diagnosis_run (project_id, task_id)
  WHERE status IN ('provisioning', 'running', 'compacting');

ALTER TABLE interaction_dag_segment DROP CONSTRAINT IF EXISTS ck_segment_source_valid;

ALTER TABLE interaction_dag_segment ADD CONSTRAINT ck_segment_source_valid
  CHECK (
    (trajectory_source = 'areal_tensor' AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
    OR
    (trajectory_source = 'task_messages' AND trajectory_id IS NULL AND tensor_ref IS NULL)
  );

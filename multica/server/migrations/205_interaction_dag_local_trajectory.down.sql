-- 205_interaction_dag_local_trajectory down: reverse dual-source column additions
-- and restore NOT NULL constraints on trajectory_id and tensor_ref.
-- Only safe when no task_messages rows exist (CHECK ensures this).

ALTER TABLE interaction_dag_segment DROP CONSTRAINT IF EXISTS ck_segment_source_valid;

ALTER TABLE interaction_dag_segment
  DROP COLUMN IF EXISTS trajectory,
  DROP COLUMN IF EXISTS trainable,
  DROP COLUMN IF EXISTS trajectory_source,
  ALTER COLUMN tensor_ref SET NOT NULL,
  ALTER COLUMN trajectory_id SET NOT NULL;

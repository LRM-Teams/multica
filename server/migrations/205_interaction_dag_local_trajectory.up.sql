-- 205_interaction_dag_local_trajectory: dual-source segment model.
-- Adds trajectory_source, trainable, trajectory columns to interaction_dag_segment,
-- makes AReaL-only columns (trajectory_id, tensor_ref) nullable, and adds CHECK
-- constraints requiring non-null AReaL fields only for areal_tensor segments and
-- null AReaL fields for task_messages segments.
-- Existing rows are backfilled as areal_tensor / trainable=true by column defaults.

ALTER TABLE interaction_dag_segment
  ALTER COLUMN trajectory_id DROP NOT NULL,
  ALTER COLUMN tensor_ref DROP NOT NULL,
  ADD COLUMN IF NOT EXISTS trajectory_source text NOT NULL DEFAULT 'areal_tensor',
  ADD COLUMN IF NOT EXISTS trainable boolean NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS trajectory jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Validate source-specific invariants:
--   areal_tensor: must have trajectory_id and tensor_ref
--   task_messages: must have no trajectory_id or tensor_ref
ALTER TABLE interaction_dag_segment ADD CONSTRAINT ck_segment_source_valid
  CHECK (
    (trajectory_source = 'areal_tensor' AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
    OR
    (trajectory_source = 'task_messages' AND trajectory_id IS NULL AND tensor_ref IS NULL)
  );

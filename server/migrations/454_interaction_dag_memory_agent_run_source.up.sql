-- Graph memory: allow the memory_agent_run trajectory source.
--
-- Submitted graph_memory_agent_run segments (agent-mode channel turns) record
-- with trajectory_source='memory_agent_run': the trajectory is supplied by the
-- run's channel evidence, not read from task_messages, and carries no AReaL
-- fields — the same invariants as the task_messages branch.

ALTER TABLE interaction_dag_segment DROP CONSTRAINT IF EXISTS ck_segment_source_valid;

ALTER TABLE interaction_dag_segment ADD CONSTRAINT ck_segment_source_valid
  CHECK (
    (trajectory_source = 'areal_tensor' AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
    OR
    (trajectory_source = 'task_messages' AND trajectory_id IS NULL AND tensor_ref IS NULL)
    OR
    (trajectory_source = 'memory_agent_run' AND trajectory_id IS NULL AND tensor_ref IS NULL)
  );

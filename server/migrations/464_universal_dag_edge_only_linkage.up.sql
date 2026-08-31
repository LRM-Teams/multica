ALTER TABLE interaction_dag_edge
  DROP CONSTRAINT interaction_dag_edge_trigger_shape_check;
ALTER TABLE interaction_dag_edge
  ADD CONSTRAINT interaction_dag_edge_trigger_shape_check
  CHECK (
    (type = 'continues' AND trigger_message_id IS NULL)
    OR
    (type IN ('responds_to', 'delegates_to', 'mentions'))
  );

CREATE OR REPLACE FUNCTION validate_universal_dag_edge()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  source_workspace_id uuid;
  source_agent_run_id uuid;
  source_start_seq integer;
  source_end_seq integer;
  target_workspace_id uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'canonical universal DAG edge deletion is forbidden';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'canonical universal DAG edge provenance is immutable';
  END IF;

  SELECT workspace_id, agent_run_id, start_seq, end_seq
    INTO source_workspace_id, source_agent_run_id, source_start_seq, source_end_seq
  FROM interaction_dag_segment
  WHERE segment_id = NEW.src_segment_id;
  IF NOT FOUND OR source_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
    RAISE EXCEPTION 'universal DAG edge source identity is invalid';
  END IF;

  SELECT workspace_id INTO target_workspace_id
  FROM interaction_dag_segment
  WHERE segment_id = NEW.dst_segment_id;
  IF NOT FOUND OR target_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
    RAISE EXCEPTION 'universal DAG edge target identity is invalid';
  END IF;

  IF NEW.type <> 'continues' AND NEW.trigger_message_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM task_message AS message
    WHERE message.id = NEW.trigger_message_id
      AND message.task_id = source_agent_run_id
      AND message.seq BETWEEN source_start_seq AND source_end_seq
  ) THEN
    RAISE EXCEPTION 'universal DAG edge trigger provenance is invalid';
  END IF;

  RETURN NEW;
END;
$$;

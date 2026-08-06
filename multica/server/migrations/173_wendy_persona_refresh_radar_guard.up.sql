-- Wendy persona refreshes are platform maintenance. They must not turn a
-- preserved workspace supervisor into an immediate Radar run.
CREATE OR REPLACE FUNCTION journal_workspace_radar_agent()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value agent%ROWTYPE;
  display_label TEXT;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF EXISTS (
    SELECT 1 FROM workspace_radar_state state
    WHERE state.workspace_id = row_value.workspace_id
      AND state.supervisor_agent_id = row_value.id
  ) THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;

  IF TG_OP = 'UPDATE' THEN
    display_label := COALESCE(NULLIF(NEW.display_name, ''), NEW.name);
    IF OLD.name IS NOT DISTINCT FROM NEW.name
       AND OLD.display_name IS NOT DISTINCT FROM NEW.display_name
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.runtime_id IS NOT DISTINCT FROM NEW.runtime_id
       AND OLD.visibility IS NOT DISTINCT FROM NEW.visibility
       AND OLD.owner_id IS NOT DISTINCT FROM NEW.owner_id
       AND OLD.archived_at IS NOT DISTINCT FROM NEW.archived_at
       AND OLD.max_concurrent_tasks IS NOT DISTINCT FROM NEW.max_concurrent_tasks
       AND (
         OLD.description IS NOT DISTINCT FROM NEW.description
         OR display_label IN ('Wendy', 'Joe')
       ) THEN
      RETURN NEW;
    END IF;
  END IF;

  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'agent', row_value.id, clock_timestamp(),
    'agent', row_value.id,
    jsonb_build_object(
      'agent_id', row_value.id,
      'name', COALESCE(NULLIF(row_value.display_name, ''), row_value.name),
      'status', CASE WHEN TG_OP = 'DELETE' THEN 'deleted' ELSE row_value.status END,
      'runtime_id', row_value.runtime_id,
      'archived_at', row_value.archived_at,
      'capabilities', left(COALESCE(row_value.description, ''), 300)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

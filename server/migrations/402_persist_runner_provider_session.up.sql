ALTER TABLE agent_runner_launch_projection
  ADD COLUMN provider_session_id TEXT;

-- A runtime move starts a different provider process and must not inherit the
-- previous runtime's session. Reconnects keep the existing projection row.
CREATE OR REPLACE FUNCTION project_agent_runner_launch()
RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    DELETE FROM agent_runner_launch_projection WHERE agent_id = OLD.id;
    RETURN OLD;
  END IF;

  IF NEW.runtime_id IS NULL OR NEW.archived_at IS NOT NULL THEN
    DELETE FROM agent_runner_launch_projection WHERE agent_id = NEW.id;
    RETURN NEW;
  END IF;

  IF TG_OP = 'INSERT'
     OR OLD.runtime_id IS DISTINCT FROM NEW.runtime_id
     OR (OLD.archived_at IS NOT NULL AND NEW.archived_at IS NULL) THEN
    INSERT INTO agent_runner_launch_projection (
      agent_id, workspace_id, runtime_id, launch_id, start_dispatch_id,
      provider_session_id
    ) VALUES (
      NEW.id, NEW.workspace_id, NEW.runtime_id,
      gen_random_uuid(), gen_random_uuid(), NULL
    )
    ON CONFLICT (agent_id) DO UPDATE SET
      workspace_id = EXCLUDED.workspace_id,
      runtime_id = EXCLUDED.runtime_id,
      launch_id = EXCLUDED.launch_id,
      start_dispatch_id = EXCLUDED.start_dispatch_id,
      provider_session_id = NULL,
      updated_at = now();
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

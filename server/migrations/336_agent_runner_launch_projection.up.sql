-- One server-owned desired launch epoch per active Agent placement. The
-- daemon reports observed residency separately through agent_activity_launch;
-- comparing these two projections drives setup, reconnect, and runtime moves.
CREATE TABLE agent_runner_launch_projection (
  agent_id UUID PRIMARY KEY REFERENCES agent(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
  launch_id UUID NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_runner_launch_projection_runtime_idx
  ON agent_runner_launch_projection (runtime_id, agent_id);

CREATE OR REPLACE FUNCTION project_agent_runner_launch()
RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    DELETE FROM agent_runner_launch_projection WHERE agent_id = OLD.id;
    RETURN OLD;
  END IF;

  IF NEW.runtime_id IS NULL OR NEW.archived_at IS NOT NULL THEN
    DELETE FROM agent_runner_launch_projection WHERE agent_id = NEW.id;
    DELETE FROM agent_start_intent WHERE agent_id = NEW.id;
    RETURN NEW;
  END IF;

  IF TG_OP = 'INSERT'
     OR OLD.runtime_id IS DISTINCT FROM NEW.runtime_id
     OR (OLD.archived_at IS NOT NULL AND NEW.archived_at IS NULL) THEN
    INSERT INTO agent_runner_launch_projection (
      agent_id, workspace_id, runtime_id, launch_id
    ) VALUES (NEW.id, NEW.workspace_id, NEW.runtime_id, gen_random_uuid())
    ON CONFLICT (agent_id) DO UPDATE SET
      workspace_id = EXCLUDED.workspace_id,
      runtime_id = EXCLUDED.runtime_id,
      launch_id = EXCLUDED.launch_id,
      updated_at = now();

    -- Rolling-deploy shadow only. New binaries never read or expose this
    -- row; an older server instance may still serve an older daemon until
    -- the deployment version floor advances.
    INSERT INTO agent_start_intent (
      start_dispatch_id, agent_id, workspace_id, runtime_id, status
    ) VALUES (gen_random_uuid(), NEW.id, NEW.workspace_id, NEW.runtime_id, 'pending')
    ON CONFLICT (agent_id) DO UPDATE SET
      start_dispatch_id = EXCLUDED.start_dispatch_id,
      workspace_id = EXCLUDED.workspace_id,
      runtime_id = EXCLUDED.runtime_id,
      status = 'pending',
      dispatch_attempts = 0,
      last_dispatched_at = NULL,
      accepted_at = NULL,
      ready_at = NULL,
      failed_at = NULL,
      failure_code = NULL,
      lifecycle_seq = 0,
      reported_at = NULL,
      updated_at = now();
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER agent_runner_launch_projection_trigger
AFTER INSERT OR UPDATE OR DELETE ON agent
FOR EACH ROW EXECUTE FUNCTION project_agent_runner_launch();

INSERT INTO agent_runner_launch_projection (
  agent_id, workspace_id, runtime_id, launch_id
)
SELECT id, workspace_id, runtime_id, gen_random_uuid()
FROM agent
WHERE runtime_id IS NOT NULL AND archived_at IS NULL;

-- `agent_start_intent`, `agent_activity_launch.start_dispatch_id`, and the
-- Attachment correlation columns remain as rolling-deploy storage shadows.
-- The new protocol and application code do not consume them. Physical DROP
-- is intentionally deferred until every server and daemon is beyond the old
-- contract; deleting them in the same release would break the still-serving
-- old binary during migration-first deployment.

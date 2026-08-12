CREATE TABLE agent_start_intent (
  start_dispatch_id UUID PRIMARY KEY,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'accepted', 'queued', 'ready', 'failed')),
  dispatch_attempts INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
  last_dispatched_at TIMESTAMPTZ,
  accepted_at TIMESTAMPTZ,
  ready_at TIMESTAMPTZ,
  failed_at TIMESTAMPTZ,
  failure_code TEXT,
  lifecycle_seq BIGINT NOT NULL DEFAULT 0 CHECK (lifecycle_seq >= 0),
  reported_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (agent_id)
);

CREATE INDEX agent_start_intent_pending_runtime_idx
  ON agent_start_intent (runtime_id, created_at, start_dispatch_id)
  WHERE status = 'pending';

INSERT INTO agent_start_intent (
  start_dispatch_id, agent_id, workspace_id, runtime_id, status
)
SELECT start_dispatch_id, agent_id, workspace_id, runtime_id, 'pending'
FROM agent_runner_launch_projection;

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

ALTER TABLE agent_runner_launch_projection
  DROP CONSTRAINT agent_runner_launch_projection_start_dispatch_key,
  DROP COLUMN start_dispatch_id;

ALTER TABLE env_dispatch_run
  ADD COLUMN status text NOT NULL DEFAULT 'provisioning',
  ADD COLUMN quiet_window_ms integer NOT NULL DEFAULT 2000,
  ADD COLUMN total_timeout_seconds integer NOT NULL DEFAULT 3300,
  ADD COLUMN initial_message_submitted_at timestamptz,
  ADD COLUMN timeout_deadline_at timestamptz,
  ADD COLUMN quiet_candidate_since timestamptz,
  ADD COLUMN active_turn_count bigint NOT NULL DEFAULT 0,
  ADD COLUMN pending_delivery_count bigint NOT NULL DEFAULT 0,
  ADD COLUMN queued_message_count bigint NOT NULL DEFAULT 0,
  ADD COLUMN inflight_tool_count bigint NOT NULL DEFAULT 0,
  ADD COLUMN unfinished_capture_batch_count bigint NOT NULL DEFAULT 0,
  ADD COLUMN capture_gap_count bigint NOT NULL DEFAULT 0,
  ADD COLUMN frozen_snapshot_id text,
  ADD COLUMN snapshot_hash text,
  ADD COLUMN frozen_at timestamptz,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
  ADD CONSTRAINT env_dispatch_run_status_check CHECK (status IN (
    'provisioning', 'preflight', 'running', 'quiet_candidate', 'freezing',
    'completed', 'failed_timeout', 'failed_preflight'
  )),
  ADD CONSTRAINT env_dispatch_run_quiet_window_check CHECK (
    quiet_window_ms BETWEEN 100 AND 60000
  ),
  ADD CONSTRAINT env_dispatch_run_total_timeout_check CHECK (
    total_timeout_seconds BETWEEN 30 AND 86400
    AND total_timeout_seconds * 1000 > quiet_window_ms
  ),
  ADD CONSTRAINT env_dispatch_run_activity_nonnegative_check CHECK (
    active_turn_count >= 0
    AND pending_delivery_count >= 0
    AND queued_message_count >= 0
    AND inflight_tool_count >= 0
    AND unfinished_capture_batch_count >= 0
    AND capture_gap_count >= 0
  ),
  ADD CONSTRAINT env_dispatch_run_quiet_candidate_check CHECK (
    status <> 'quiet_candidate'
    OR (
      active_turn_count = 0
      AND pending_delivery_count = 0
      AND queued_message_count = 0
      AND inflight_tool_count = 0
      AND unfinished_capture_batch_count = 0
      AND quiet_candidate_since IS NOT NULL
    )
  ),
  ADD CONSTRAINT env_dispatch_run_timeout_origin_check CHECK (
    (
      status IN ('provisioning', 'preflight', 'failed_preflight')
      AND initial_message_submitted_at IS NULL
      AND timeout_deadline_at IS NULL
    )
    OR (
      status IN ('running', 'quiet_candidate', 'freezing', 'completed', 'failed_timeout')
      AND initial_message_submitted_at IS NOT NULL
      AND timeout_deadline_at IS NOT NULL
      AND timeout_deadline_at > initial_message_submitted_at
    )
  ),
  ADD CONSTRAINT env_dispatch_run_terminal_snapshot_check CHECK (
    status NOT IN ('completed', 'failed_timeout')
    OR (
      frozen_snapshot_id IS NOT NULL
      AND snapshot_hash IS NOT NULL
      AND frozen_at IS NOT NULL
    )
  );

CREATE TABLE env_dispatch_run_agent (
  run_agent_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL REFERENCES env_dispatch_run(run_id) ON DELETE CASCADE,
  source_agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
  execution_agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
  runtime_id uuid NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
  pi_session_id text NOT NULL CHECK (length(btrim(pi_session_id)) > 0),
  training_mode text NOT NULL CHECK (training_mode IN ('online_rl', 'offline_rl', 'none')),
  areal_session_id text CHECK (
    areal_session_id IS NULL OR length(btrim(areal_session_id)) > 0
  ),
  CONSTRAINT env_dispatch_run_agent_mode_identity_check CHECK (
    training_mode <> 'none' OR areal_session_id IS NULL
  ),
  capture_boundary text NOT NULL CHECK (length(btrim(capture_boundary)) > 0),
  next_turn_ordinal bigint NOT NULL DEFAULT 1 CHECK (next_turn_ordinal > 0),
  next_call_ordinal bigint NOT NULL DEFAULT 1 CHECK (next_call_ordinal > 0),
  settled_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, source_agent_id),
  UNIQUE (run_id, execution_agent_id),
  UNIQUE (run_id, pi_session_id),
  UNIQUE (run_id, run_agent_id)
);

CREATE INDEX env_dispatch_run_agent_source_idx
  ON env_dispatch_run_agent (source_agent_id);
CREATE INDEX env_dispatch_run_agent_execution_idx
  ON env_dispatch_run_agent (execution_agent_id);

-- Idempotent server-observed activity transitions. These establish the
-- exactly-once transition identity used to update the five run counters.
CREATE TABLE env_dispatch_activity_transition (
  run_id uuid NOT NULL,
  transition_id text NOT NULL CHECK (length(btrim(transition_id)) > 0),
  run_agent_id uuid NOT NULL,
  agent_id uuid NOT NULL,
  runtime_id uuid NOT NULL,
  dimension text NOT NULL CHECK (dimension IN (
    'active_turn', 'queued_message', 'inflight_tool', 'unfinished_capture_batch'
  )),
  delta smallint NOT NULL CHECK (delta IN (-1, 1)),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, transition_id),
  FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id) ON DELETE CASCADE
);

CREATE INDEX env_dispatch_activity_transition_agent_idx
  ON env_dispatch_activity_transition (run_id, run_agent_id, created_at);

CREATE OR REPLACE FUNCTION validate_env_dispatch_run_agent_readiness()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  parent_status text;
BEGIN
  IF NEW.training_mode = 'none' AND NEW.areal_session_id IS NOT NULL THEN
    RAISE EXCEPTION 'none run-agent cannot carry an AReaL session';
  END IF;

  IF NEW.training_mode <> 'online_rl' OR NEW.areal_session_id IS NOT NULL THEN
    RETURN NEW;
  END IF;

  SELECT status
  INTO parent_status
  FROM env_dispatch_run
  WHERE run_id = NEW.run_id
  FOR KEY SHARE;

  IF parent_status IN ('running', 'quiet_candidate', 'freezing', 'completed', 'failed_timeout') THEN
    RAISE EXCEPTION 'online_rl run-agent requires an AReaL session before active lifecycle';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER env_dispatch_run_agent_readiness_guard
BEFORE INSERT OR UPDATE OF training_mode, areal_session_id ON env_dispatch_run_agent
FOR EACH ROW EXECUTE FUNCTION validate_env_dispatch_run_agent_readiness();
CREATE OR REPLACE FUNCTION validate_env_dispatch_run_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.status = NEW.status THEN
    RETURN NEW;
  END IF;

  IF OLD.status IN ('completed', 'failed_timeout', 'failed_preflight') THEN
    RAISE EXCEPTION 'terminal env dispatch run status % is immutable', OLD.status;
  END IF;

  IF NEW.status IN ('running', 'quiet_candidate', 'freezing', 'completed', 'failed_timeout')
     AND EXISTS (
       SELECT 1
       FROM env_dispatch_run_agent agent
       WHERE agent.run_id = NEW.run_id
         AND agent.training_mode = 'online_rl'
         AND agent.areal_session_id IS NULL
     ) THEN
    RAISE EXCEPTION 'online_rl run-agent requires an AReaL session before active lifecycle';
  END IF;

  IF NOT (
    (OLD.status = 'provisioning' AND NEW.status = 'preflight')
    OR (OLD.status = 'preflight' AND NEW.status IN ('running', 'failed_preflight'))
    OR (OLD.status = 'running' AND NEW.status IN ('quiet_candidate', 'freezing'))
    OR (OLD.status = 'quiet_candidate' AND NEW.status IN ('running', 'freezing'))
    OR (OLD.status = 'freezing' AND NEW.status IN ('completed', 'failed_timeout'))
  ) THEN
    RAISE EXCEPTION 'invalid env dispatch run status transition % -> %', OLD.status, NEW.status;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER env_dispatch_run_status_transition_guard
BEFORE UPDATE OF status ON env_dispatch_run
FOR EACH ROW EXECUTE FUNCTION validate_env_dispatch_run_status_transition();

CREATE OR REPLACE FUNCTION reject_terminal_env_dispatch_run_snapshot_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.status IN ('completed', 'failed_timeout')
     AND (
       NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.project_id IS DISTINCT FROM OLD.project_id
       OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.initial_message_submitted_at IS DISTINCT FROM OLD.initial_message_submitted_at
       OR NEW.timeout_deadline_at IS DISTINCT FROM OLD.timeout_deadline_at
       OR NEW.frozen_snapshot_id IS DISTINCT FROM OLD.frozen_snapshot_id
       OR NEW.snapshot_hash IS DISTINCT FROM OLD.snapshot_hash
       OR NEW.frozen_at IS DISTINCT FROM OLD.frozen_at
       OR NEW.capture_gap_count IS DISTINCT FROM OLD.capture_gap_count
     ) THEN
    RAISE EXCEPTION 'terminal env dispatch run snapshot metadata is immutable';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER env_dispatch_run_terminal_snapshot_immutable
BEFORE UPDATE OF
  run_id, project_id, workspace_id, initial_message_submitted_at,
  timeout_deadline_at, frozen_snapshot_id, snapshot_hash, frozen_at,
  capture_gap_count
ON env_dispatch_run
FOR EACH ROW EXECUTE FUNCTION reject_terminal_env_dispatch_run_snapshot_mutation();

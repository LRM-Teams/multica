CREATE TABLE env_dispatch_resident_turn (
  turn_id uuid PRIMARY KEY,
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  turn_ordinal bigint NOT NULL CHECK (turn_ordinal > 0),
  status text NOT NULL CHECK (status IN ('active', 'settled', 'failed', 'aborted', 'capture_gap')),
  capture_started_at timestamptz,
  capture_completed_at timestamptz,
  accepted_message_ids uuid[] NOT NULL DEFAULT '{}',
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE (run_agent_id, turn_ordinal),
  UNIQUE (run_id, run_agent_id, turn_id),
  FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id) ON DELETE CASCADE
);

CREATE TABLE env_dispatch_turn_capture_batch (
  capture_batch_id uuid PRIMARY KEY,
  turn_id uuid NOT NULL UNIQUE REFERENCES env_dispatch_resident_turn(turn_id) ON DELETE CASCADE,
  capture_boundary text NOT NULL,
  call_count integer NOT NULL CHECK (call_count >= 0),
  action_count integer NOT NULL CHECK (action_count >= 0),
  consumption_count integer NOT NULL CHECK (consumption_count >= 0),
  payload_hash text NOT NULL CHECK (length(btrim(payload_hash)) > 0),
  accepted_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION validate_mixed_rl_capture_batch_boundary()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM env_dispatch_resident_turn turn
    JOIN env_dispatch_run_agent agent
      ON agent.run_id = turn.run_id
     AND agent.run_agent_id = turn.run_agent_id
    WHERE turn.turn_id = NEW.turn_id
      AND agent.capture_boundary = NEW.capture_boundary
  ) THEN
    RAISE EXCEPTION 'capture batch boundary does not match the run-agent boundary';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER env_dispatch_turn_capture_batch_boundary_guard
AFTER INSERT OR UPDATE OF turn_id, capture_boundary ON env_dispatch_turn_capture_batch
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_mixed_rl_capture_batch_boundary();

CREATE TABLE env_dispatch_delivery_obligation (
  delivery_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL,

  channel_message_id uuid NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  source_recipient_agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
  run_agent_id uuid NOT NULL,
  state text NOT NULL CHECK (state IN ('pending', 'queued', 'accepted', 'completed', 'failed', 'cancelled')),
  queued_at timestamptz,
  settled_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (channel_message_id, run_agent_id),
  FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id) ON DELETE CASCADE
);

CREATE INDEX env_dispatch_delivery_obligation_pending_idx
  ON env_dispatch_delivery_obligation (run_id, state)
  WHERE state IN ('pending', 'queued', 'accepted');

CREATE TABLE pi_provider_call (
  call_id text PRIMARY KEY CHECK (length(btrim(call_id)) > 0),
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  turn_id uuid NOT NULL,
  pi_session_id text NOT NULL,
  call_ordinal bigint NOT NULL CHECK (call_ordinal > 0),
  provider text NOT NULL,
  model text NOT NULL,
  api_kind text NOT NULL,
  raw_provider_request jsonb NOT NULL CHECK (jsonb_typeof(raw_provider_request) = 'object'),
  final_assistant_message jsonb NOT NULL CHECK (jsonb_typeof(final_assistant_message) = 'object'),
  normalized_trajectory jsonb,
  normalization_version text,
  status text NOT NULL CHECK (status IN (
    'in_progress', 'completed', 'length', 'error', 'aborted', 'timeout',
    'crash', 'stream_interrupted'
  )),
  stop_reason text,
  response_complete boolean NOT NULL DEFAULT false,
  training_eligible boolean NOT NULL DEFAULT false,
  areal_session_id text,
  areal_call_id text,
  request_hash text NOT NULL,
  response_hash text,
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  frozen_at timestamptz,
  UNIQUE (run_agent_id, call_ordinal),
  UNIQUE (run_id, call_id),
  UNIQUE (run_id, run_agent_id, call_id),
  FOREIGN KEY (run_id, run_agent_id, turn_id)
    REFERENCES env_dispatch_resident_turn(run_id, run_agent_id, turn_id) ON DELETE CASCADE,
  CONSTRAINT pi_provider_call_normalization_check CHECK (
    normalized_trajectory IS NULL OR normalization_version IS NOT NULL
  ),
  CONSTRAINT pi_provider_call_online_identity_check CHECK (
    (areal_session_id IS NULL AND areal_call_id IS NULL)
    OR (areal_session_id IS NOT NULL AND areal_call_id IS NOT NULL)
  ),
  CONSTRAINT pi_provider_call_training_eligibility_check CHECK (
    training_eligible = (
      status = 'completed'
      AND response_complete
      AND stop_reason IN ('stop', 'toolUse')
    )
  )
);

CREATE OR REPLACE FUNCTION validate_mixed_rl_provider_call_identity()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  parent_training_mode text;
  parent_pi_session_id text;
  parent_areal_session_id text;
BEGIN
  SELECT training_mode, pi_session_id, areal_session_id
  INTO parent_training_mode, parent_pi_session_id, parent_areal_session_id
  FROM env_dispatch_run_agent
  WHERE run_id = NEW.run_id
    AND run_agent_id = NEW.run_agent_id
  FOR KEY SHARE;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'provider call requires a matching run-agent';
  END IF;
  IF NEW.pi_session_id IS DISTINCT FROM parent_pi_session_id THEN
    RAISE EXCEPTION 'provider call Pi session must match its run-agent';
  END IF;
  IF parent_training_mode = 'online_rl' THEN
    IF parent_areal_session_id IS NULL
       OR NEW.areal_session_id IS DISTINCT FROM parent_areal_session_id
       OR NEW.areal_call_id IS NULL THEN
      RAISE EXCEPTION 'online_rl provider call requires its run-agent AReaL session and a call identity';
    END IF;
  ELSIF NEW.areal_session_id IS NOT NULL OR NEW.areal_call_id IS NOT NULL THEN
    RAISE EXCEPTION 'non-online provider call cannot carry AReaL identity';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER pi_provider_call_identity_guard
BEFORE INSERT OR UPDATE OF run_id, run_agent_id, pi_session_id, areal_session_id, areal_call_id
ON pi_provider_call
FOR EACH ROW EXECUTE FUNCTION validate_mixed_rl_provider_call_identity();

CREATE UNIQUE INDEX pi_provider_call_areal_identity_uidx
  ON pi_provider_call (areal_session_id, areal_call_id)
  WHERE areal_session_id IS NOT NULL AND areal_call_id IS NOT NULL;
CREATE INDEX pi_provider_call_canonical_idx
  ON pi_provider_call (run_id, run_agent_id, call_ordinal);

CREATE TABLE pi_visible_action (
  action_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  turn_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('message', 'reaction')),
  canonical_id uuid NOT NULL,
  producer_call_id text,
  action_ordinal bigint NOT NULL CHECK (action_ordinal > 0),
  status text NOT NULL CHECK (status IN ('succeeded', 'failed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (kind, canonical_id),
  UNIQUE (run_id, run_agent_id, action_id),
  FOREIGN KEY (run_id, run_agent_id, turn_id)
    REFERENCES env_dispatch_resident_turn(run_id, run_agent_id, turn_id) ON DELETE CASCADE,
  FOREIGN KEY (run_id, run_agent_id, producer_call_id)
    REFERENCES pi_provider_call(run_id, run_agent_id, call_id)
);

CREATE TABLE pi_message_consumption (
  consumption_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  turn_id uuid NOT NULL,
  channel_message_id uuid NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  source text NOT NULL CHECK (source IN ('accept_message_batch', 'message_check')),
  effective_from_call_id text NOT NULL,
  consumed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_agent_id, channel_message_id, effective_from_call_id),
  FOREIGN KEY (run_id, run_agent_id, turn_id)
    REFERENCES env_dispatch_resident_turn(run_id, run_agent_id, turn_id) ON DELETE CASCADE,
  FOREIGN KEY (run_id, run_agent_id, effective_from_call_id)
    REFERENCES pi_provider_call(run_id, run_agent_id, call_id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION validate_mixed_rl_consumption_effective_call()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pi_provider_call call
    WHERE call.run_id = NEW.run_id
      AND call.run_agent_id = NEW.run_agent_id
      AND call.call_id = NEW.effective_from_call_id
      AND call.started_at > NEW.consumed_at
  ) THEN
    RAISE EXCEPTION 'consumption effective call must belong to the same run-agent and start after consumption';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER pi_message_consumption_effective_call_guard
AFTER INSERT OR UPDATE ON pi_message_consumption
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_mixed_rl_consumption_effective_call();

CREATE TABLE env_dispatch_run_audit_event (
  event_id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES env_dispatch_run(run_id) ON DELETE CASCADE,
  run_agent_id uuid,
  turn_id uuid,
  kind text NOT NULL CHECK (kind IN ('capture_gap', 'late_event')),
  reason text NOT NULL CHECK (length(btrim(reason)) > 0),
  summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(summary) = 'object'),
  snapshot_id text,
  received_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT env_dispatch_run_audit_event_turn_scope_check CHECK (
    turn_id IS NULL OR run_agent_id IS NOT NULL
  ),
  CONSTRAINT env_dispatch_run_audit_event_late_snapshot_check CHECK (
    kind <> 'late_event' OR snapshot_id IS NOT NULL
  ),
  FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id) ON DELETE CASCADE,
  FOREIGN KEY (run_id, run_agent_id, turn_id)
    REFERENCES env_dispatch_resident_turn(run_id, run_agent_id, turn_id) ON DELETE CASCADE
);

CREATE INDEX env_dispatch_run_audit_event_order_idx
  ON env_dispatch_run_audit_event (run_id, received_at, event_id);

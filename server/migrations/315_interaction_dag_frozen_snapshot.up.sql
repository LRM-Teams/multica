CREATE TABLE interaction_dag_frozen_snapshot (
  snapshot_id text PRIMARY KEY CHECK (length(btrim(snapshot_id)) > 0),
  run_id uuid NOT NULL UNIQUE REFERENCES env_dispatch_run(run_id) ON DELETE CASCADE,
  run_status text NOT NULL CHECK (run_status IN ('completed', 'failed_timeout')),
  schema_version text NOT NULL,
  normalization_version text NOT NULL,
  segment_count bigint NOT NULL DEFAULT 0 CHECK (segment_count >= 0),
  call_count bigint NOT NULL DEFAULT 0 CHECK (call_count >= 0),
  edge_count bigint NOT NULL DEFAULT 0 CHECK (edge_count >= 0),
  canonical_manifest jsonb NOT NULL CHECK (jsonb_typeof(canonical_manifest) = 'object'),
  snapshot_hash text NOT NULL CHECK (length(btrim(snapshot_hash)) > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, snapshot_id)
);

ALTER TABLE env_dispatch_run
  ADD CONSTRAINT env_dispatch_run_frozen_snapshot_fk
  FOREIGN KEY (run_id, frozen_snapshot_id)
  REFERENCES interaction_dag_frozen_snapshot(run_id, snapshot_id)
  DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE env_dispatch_run_audit_event
  ADD CONSTRAINT env_dispatch_run_audit_event_snapshot_fk
  FOREIGN KEY (run_id, snapshot_id)
  REFERENCES interaction_dag_frozen_snapshot(run_id, snapshot_id)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE interaction_dag_run_segment (
  segment_id text PRIMARY KEY CHECK (length(btrim(segment_id)) > 0),
  snapshot_id text,
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('message', 'reaction', 'terminal')),
  canonical_action_id uuid,
  segment_ordinal bigint NOT NULL CHECK (segment_ordinal > 0),
  reward double precision,
  reward_source text,
  provisional_at timestamptz NOT NULL DEFAULT now(),
  finalized_at timestamptz,
  UNIQUE (snapshot_id, segment_ordinal),
  UNIQUE (run_id, segment_id),
  UNIQUE (run_id, run_agent_id, segment_id),
  FOREIGN KEY (run_id, snapshot_id)
    REFERENCES interaction_dag_frozen_snapshot(run_id, snapshot_id) ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id) ON DELETE CASCADE,
  CONSTRAINT interaction_dag_run_segment_action_check CHECK (
    (kind = 'terminal' AND canonical_action_id IS NULL)
    OR (kind IN ('message', 'reaction') AND canonical_action_id IS NOT NULL)
  )
);

CREATE UNIQUE INDEX interaction_dag_one_terminal_per_run_agent_uidx
  ON interaction_dag_run_segment (run_id, run_agent_id)
  WHERE kind = 'terminal';

CREATE TABLE interaction_dag_segment_provider_call (
  segment_id text NOT NULL,
  provider_call_id text NOT NULL,
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  call_ordinal bigint NOT NULL CHECK (call_ordinal > 0),
  association_kind text NOT NULL CHECK (association_kind IN ('owned', 'shared_producer', 'audit')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (segment_id, provider_call_id),
  FOREIGN KEY (run_id, run_agent_id, segment_id)
    REFERENCES interaction_dag_run_segment(run_id, run_agent_id, segment_id) ON DELETE CASCADE,
  FOREIGN KEY (run_id, run_agent_id, provider_call_id)
    REFERENCES pi_provider_call(run_id, run_agent_id, call_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX interaction_dag_provider_call_one_owner_uidx
  ON interaction_dag_segment_provider_call (provider_call_id)
  WHERE association_kind = 'owned';

CREATE OR REPLACE FUNCTION validate_mixed_rl_shared_producer_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_call_id text;
  scoped_run_id uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    scoped_call_id := OLD.provider_call_id;
    scoped_run_id := OLD.run_id;
  ELSE
    scoped_call_id := NEW.provider_call_id;
    scoped_run_id := NEW.run_id;
  END IF;

  IF EXISTS (
    SELECT 1
    FROM interaction_dag_segment_provider_call association
    WHERE association.provider_call_id = scoped_call_id
      AND association.run_id = scoped_run_id
      AND association.association_kind = 'shared_producer'
  ) AND NOT EXISTS (
    SELECT 1
    FROM interaction_dag_segment_provider_call association
    WHERE association.provider_call_id = scoped_call_id
      AND association.run_id = scoped_run_id
      AND association.association_kind = 'owned'
  ) THEN
    RAISE EXCEPTION 'shared_producer requires a same-run owned association';
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER interaction_dag_shared_producer_owner_guard
AFTER INSERT OR UPDATE OR DELETE ON interaction_dag_segment_provider_call
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_mixed_rl_shared_producer_owner();

CREATE TABLE interaction_dag_causal_edge (
  edge_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  snapshot_id text,
  run_id uuid NOT NULL,
  src_segment_id text NOT NULL,
  dst_segment_id text NOT NULL,
  type text NOT NULL CHECK (type IN ('channel_message', 'reaction', 'session_continuation')),
  trigger_message_id uuid,
  dst_call_id text,
  edge_ordinal bigint NOT NULL CHECK (edge_ordinal > 0),
  UNIQUE (snapshot_id, edge_ordinal),
  UNIQUE (snapshot_id, src_segment_id, dst_segment_id, type, trigger_message_id, dst_call_id),
  FOREIGN KEY (run_id, snapshot_id)
    REFERENCES interaction_dag_frozen_snapshot(run_id, snapshot_id) ON DELETE CASCADE
    DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY (run_id, src_segment_id)
    REFERENCES interaction_dag_run_segment(run_id, segment_id) ON DELETE CASCADE,
  FOREIGN KEY (run_id, dst_segment_id)
    REFERENCES interaction_dag_run_segment(run_id, segment_id) ON DELETE CASCADE,
  FOREIGN KEY (run_id, dst_call_id)
    REFERENCES pi_provider_call(run_id, call_id) ON DELETE CASCADE,
  CONSTRAINT interaction_dag_causal_edge_fields_check CHECK (
    (type = 'channel_message' AND trigger_message_id IS NOT NULL AND dst_call_id IS NOT NULL)
    OR (type = 'reaction' AND trigger_message_id IS NOT NULL)
    OR (type = 'session_continuation' AND trigger_message_id IS NULL AND dst_call_id IS NOT NULL)
  )
);

CREATE OR REPLACE FUNCTION reject_frozen_snapshot_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'frozen interaction snapshots are immutable';
END;
$$;

CREATE TRIGGER interaction_dag_frozen_snapshot_immutable
BEFORE UPDATE ON interaction_dag_frozen_snapshot
FOR EACH ROW EXECUTE FUNCTION reject_frozen_snapshot_update();

CREATE OR REPLACE FUNCTION enforce_mixed_rl_graph_mutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_run_id uuid;
  scoped_run_ids uuid[];
  scoped_status text;
  scoped_snapshot_id text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    scoped_run_ids := ARRAY[NEW.run_id];
  ELSIF TG_OP = 'DELETE' THEN
    scoped_run_ids := ARRAY[OLD.run_id];
  ELSE
    scoped_run_ids := ARRAY[OLD.run_id];
    IF NEW.run_id IS DISTINCT FROM OLD.run_id THEN
      scoped_run_ids := array_append(scoped_run_ids, NEW.run_id);
    END IF;
  END IF;

  FOREACH scoped_run_id IN ARRAY scoped_run_ids LOOP
    SELECT status, frozen_snapshot_id
    INTO scoped_status, scoped_snapshot_id
    FROM env_dispatch_run
    WHERE run_id = scoped_run_id
    FOR KEY SHARE;

    IF FOUND
       AND (scoped_status IN ('freezing', 'completed', 'failed_timeout') OR scoped_snapshot_id IS NOT NULL)
       AND current_setting('multica.mixed_rl_freeze_writer', true) IS DISTINCT FROM 'on' THEN
      RAISE EXCEPTION 'mixed-RL graph for run % is frozen', scoped_run_id;
    END IF;
  END LOOP;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER env_dispatch_run_agent_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON env_dispatch_run_agent
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

CREATE TRIGGER pi_provider_call_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON pi_provider_call
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

CREATE TRIGGER pi_visible_action_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON pi_visible_action
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

CREATE TRIGGER pi_message_consumption_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON pi_message_consumption
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

CREATE OR REPLACE FUNCTION enforce_mixed_rl_audit_event_lifecycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_status text;
  scoped_snapshot_id text;
BEGIN
  IF TG_OP IN ('UPDATE', 'DELETE') AND OLD.kind = 'capture_gap' THEN
    SELECT status
    INTO scoped_status
    FROM env_dispatch_run
    WHERE run_id = OLD.run_id
    FOR KEY SHARE;

    IF FOUND AND scoped_status IN ('completed', 'failed_timeout') THEN
      RAISE EXCEPTION 'frozen capture gap for run % is immutable', OLD.run_id;
    END IF;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;

  SELECT status, frozen_snapshot_id
  INTO scoped_status, scoped_snapshot_id
  FROM env_dispatch_run
  WHERE run_id = NEW.run_id
  FOR KEY SHARE;

  IF NEW.kind = 'capture_gap'
     AND scoped_status IN ('freezing', 'completed', 'failed_timeout', 'failed_preflight') THEN
    RAISE EXCEPTION 'capture gap for run % arrived after capture closure', NEW.run_id;
  END IF;

  IF NEW.kind = 'late_event'
     AND (
       scoped_status NOT IN ('completed', 'failed_timeout')
       OR scoped_snapshot_id IS NULL
       OR NEW.snapshot_id IS DISTINCT FROM scoped_snapshot_id
     ) THEN
    RAISE EXCEPTION 'late event for run % must reference its terminal snapshot', NEW.run_id;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER env_dispatch_run_audit_event_lifecycle_guard
BEFORE INSERT OR UPDATE OR DELETE ON env_dispatch_run_audit_event
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_audit_event_lifecycle();

CREATE TRIGGER interaction_dag_run_segment_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON interaction_dag_run_segment
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

CREATE TRIGGER interaction_dag_segment_provider_call_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON interaction_dag_segment_provider_call
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

CREATE TRIGGER interaction_dag_causal_edge_freeze_guard
BEFORE INSERT OR UPDATE OR DELETE ON interaction_dag_causal_edge
FOR EACH ROW EXECUTE FUNCTION enforce_mixed_rl_graph_mutability();

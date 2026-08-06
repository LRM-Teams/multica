-- Bind asynchronous Research Attempts to every half-open circuit they own.
-- The binding survives process restarts and lets result/failure/cancellation
-- settlement prove the exact token and generation it is allowed to resolve.

ALTER TABLE research_execution_circuit_transition
  DROP CONSTRAINT research_execution_circuit_transition_cause_check,
  ADD CONSTRAINT research_execution_circuit_transition_cause_check
  CHECK (cause IN (
    'failure_observed', 'configuration_changed', 'probe_claimed',
    'probe_succeeded', 'probe_failed', 'probe_abandoned',
    'probe_inconclusive', 'success_observed'
  ));

CREATE TABLE research_attempt_circuit_probe (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  attempt_id UUID NOT NULL REFERENCES research_task_attempt(id) ON DELETE CASCADE,
  circuit_id UUID NOT NULL REFERENCES research_execution_circuit(id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (scope IN ('agent', 'runtime', 'provider', 'adapter')),
  probe_token UUID NOT NULL,
  generation BIGINT NOT NULL CHECK (generation >= 1),
  config_fingerprint TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'succeeded', 'failed', 'inconclusive', 'abandoned', 'lost')),
  failure_class TEXT NOT NULL DEFAULT '',
  source_reason TEXT NOT NULL DEFAULT '',
  diagnostics TEXT NOT NULL DEFAULT '',
  resolved_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (attempt_id, circuit_id),
  CONSTRAINT research_attempt_circuit_probe_resolution_check CHECK (
    (status = 'active' AND resolved_at IS NULL)
    OR (status <> 'active' AND resolved_at IS NOT NULL)
  )
);

CREATE UNIQUE INDEX research_attempt_circuit_probe_active_circuit_uidx
  ON research_attempt_circuit_probe (circuit_id)
  WHERE status = 'active';
CREATE INDEX research_attempt_circuit_probe_attempt_idx
  ON research_attempt_circuit_probe (attempt_id, status, circuit_id);

CREATE OR REPLACE FUNCTION research_attempt_circuit_probe_guard()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' AND NOT EXISTS (
    SELECT 1
    FROM research_task_attempt attempt
    JOIN research_execution_circuit circuit ON circuit.id = NEW.circuit_id
    WHERE attempt.id = NEW.attempt_id
      AND attempt.workspace_id = NEW.workspace_id
      AND attempt.session_id = NEW.session_id
      AND circuit.workspace_id = NEW.workspace_id
      AND circuit.scope = NEW.scope
      AND circuit.config_fingerprint = NEW.config_fingerprint
      AND circuit.state = 'half_open'
      AND circuit.probe_token = NEW.probe_token
      AND circuit.generation = NEW.generation
      AND circuit.probe_lease_expires_at = NEW.lease_expires_at
  ) THEN
    RAISE EXCEPTION 'research attempt probe does not match its attempt and circuit owner'
      USING ERRCODE = '23514', CONSTRAINT = 'research_attempt_circuit_probe_owner_check';
  END IF;

  IF TG_OP = 'UPDATE' AND (
    NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
    OR NEW.session_id IS DISTINCT FROM OLD.session_id
    OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
    OR NEW.circuit_id IS DISTINCT FROM OLD.circuit_id
    OR NEW.scope IS DISTINCT FROM OLD.scope
    OR NEW.probe_token IS DISTINCT FROM OLD.probe_token
    OR NEW.generation IS DISTINCT FROM OLD.generation
    OR NEW.config_fingerprint IS DISTINCT FROM OLD.config_fingerprint
    OR NEW.lease_expires_at IS DISTINCT FROM OLD.lease_expires_at
  ) THEN
    RAISE EXCEPTION 'research attempt probe identity is immutable'
      USING ERRCODE = '23514', CONSTRAINT = 'research_attempt_circuit_probe_identity_immutable_check';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER research_attempt_circuit_probe_guard_trigger
BEFORE INSERT OR UPDATE ON research_attempt_circuit_probe
FOR EACH ROW EXECUTE FUNCTION research_attempt_circuit_probe_guard();

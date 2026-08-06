-- Workspace-bound execution health for Research Runs. Agent display quota
-- fields remain a separate product fact; this state owns failure windows,
-- half-open probe ownership, and auditable recovery decisions.

ALTER TABLE research_task_attempt
  ADD COLUMN agent_config_fingerprint TEXT NOT NULL DEFAULT '',
  ADD COLUMN runtime_config_fingerprint TEXT NOT NULL DEFAULT '',
  ADD COLUMN provider_config_fingerprint TEXT NOT NULL DEFAULT '';

UPDATE research_task_attempt
SET agent_config_fingerprint = target_config_fingerprint,
    runtime_config_fingerprint = target_config_fingerprint,
    provider_config_fingerprint = target_config_fingerprint
WHERE target_config_fingerprint <> '';

CREATE OR REPLACE FUNCTION research_attempt_execution_target_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.execution_adapter IS DISTINCT FROM OLD.execution_adapter
     OR NEW.runtime_id IS DISTINCT FROM OLD.runtime_id
     OR NEW.provider IS DISTINCT FROM OLD.provider
     OR NEW.model IS DISTINCT FROM OLD.model
     OR NEW.target_config_fingerprint IS DISTINCT FROM OLD.target_config_fingerprint
     OR NEW.agent_config_fingerprint IS DISTINCT FROM OLD.agent_config_fingerprint
     OR NEW.runtime_config_fingerprint IS DISTINCT FROM OLD.runtime_config_fingerprint
     OR NEW.provider_config_fingerprint IS DISTINCT FROM OLD.provider_config_fingerprint THEN
    RAISE EXCEPTION 'research attempt execution target is immutable'
      USING ERRCODE = '23514', CONSTRAINT = 'research_task_attempt_execution_target_immutable_check';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS research_task_attempt_execution_target_immutable_guard
  ON research_task_attempt;
CREATE TRIGGER research_task_attempt_execution_target_immutable_guard
BEFORE UPDATE OF execution_adapter, runtime_id, provider, model,
  target_config_fingerprint, agent_config_fingerprint,
  runtime_config_fingerprint, provider_config_fingerprint
ON research_task_attempt
FOR EACH ROW EXECUTE FUNCTION research_attempt_execution_target_immutable();

CREATE TABLE research_execution_circuit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (scope IN ('agent', 'runtime', 'provider', 'adapter')),
  target_key TEXT NOT NULL CHECK (btrim(target_key) <> ''),
  target_label TEXT NOT NULL DEFAULT '',
  config_fingerprint TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'closed' CHECK (state IN ('closed', 'open', 'half_open')),
  generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
  consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
  window_started_at TIMESTAMPTZ,
  opened_at TIMESTAMPTZ,
  next_probe_at TIMESTAMPTZ,
  probe_token UUID,
  probe_lease_expires_at TIMESTAMPTZ,
  last_failure_class TEXT NOT NULL DEFAULT '',
  last_source_reason TEXT NOT NULL DEFAULT '',
  last_diagnostics TEXT NOT NULL DEFAULT '',
  last_attempt_id UUID REFERENCES research_task_attempt(id) ON DELETE SET NULL,
  last_session_id UUID REFERENCES research_session(id) ON DELETE SET NULL,
  last_failed_at TIMESTAMPTZ,
  last_succeeded_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, scope, target_key),
  CONSTRAINT research_execution_circuit_probe_pair_check CHECK (
    (probe_token IS NULL AND probe_lease_expires_at IS NULL)
    OR (probe_token IS NOT NULL AND probe_lease_expires_at IS NOT NULL)
  ),
  CONSTRAINT research_execution_circuit_half_open_owner_check CHECK (
    (state = 'half_open' AND probe_token IS NOT NULL)
    OR (state <> 'half_open' AND probe_token IS NULL)
  ),
  CONSTRAINT research_execution_circuit_probe_time_check CHECK (
    (state = 'closed' AND next_probe_at IS NULL)
    OR (state IN ('open', 'half_open') AND next_probe_at IS NOT NULL)
  )
);

CREATE INDEX research_execution_circuit_probe_due_idx
  ON research_execution_circuit (next_probe_at, workspace_id, scope, target_key)
  WHERE state = 'open';
CREATE INDEX research_execution_circuit_probe_lease_idx
  ON research_execution_circuit (probe_lease_expires_at, workspace_id, scope, target_key)
  WHERE state = 'half_open';

CREATE TABLE research_execution_circuit_transition (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  circuit_id UUID NOT NULL REFERENCES research_execution_circuit(id) ON DELETE CASCADE,
  session_id UUID REFERENCES research_session(id) ON DELETE SET NULL,
  attempt_id UUID REFERENCES research_task_attempt(id) ON DELETE SET NULL,
  generation BIGINT NOT NULL CHECK (generation >= 1),
  from_state TEXT NOT NULL CHECK (from_state IN ('closed', 'open', 'half_open')),
  to_state TEXT NOT NULL CHECK (to_state IN ('closed', 'open', 'half_open')),
  cause TEXT NOT NULL CHECK (cause IN (
    'failure_observed', 'configuration_changed', 'probe_claimed',
    'probe_succeeded', 'probe_failed', 'success_observed'
  )),
  failure_class TEXT NOT NULL DEFAULT '',
  source_reason TEXT NOT NULL DEFAULT '',
  diagnostics TEXT NOT NULL DEFAULT '',
  config_fingerprint TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (circuit_id, generation)
);

CREATE INDEX research_execution_circuit_transition_session_idx
  ON research_execution_circuit_transition (session_id, created_at, id)
  WHERE session_id IS NOT NULL;

CREATE UNIQUE INDEX research_execution_circuit_transition_attempt_observation_uidx
  ON research_execution_circuit_transition (circuit_id, attempt_id, cause)
  WHERE attempt_id IS NOT NULL AND cause IN ('failure_observed', 'success_observed');

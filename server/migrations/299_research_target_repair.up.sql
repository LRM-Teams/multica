-- Target-idempotent repair records for Research Run execution failures.
--
-- The repair key is the canonical identity of "this Task, at this state
-- version, already has a decided repair for this exact execution failure".
-- Recomputing the same canonical failure reuses the row and only advances the
-- observation counters; a new repair may exist only when the state version,
-- the frozen target configuration, or the failure class changes. This makes
-- "create the same remediation Task again" impossible by construction rather
-- than by executor discipline.

-- The allowed action matrix is an immutable database judgement so a Handler,
-- background job, or manual SQL statement cannot persist a repair action that
-- the failure class does not permit. Research results (research_negative,
-- method_invalid) are not execution failures and therefore have no allowed
-- repair action at all: they must never be recorded here.
CREATE OR REPLACE FUNCTION research_repair_action_allowed(
  failure_class TEXT,
  repair_kind TEXT
)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT (failure_class, repair_kind) IN (
    ('runtime_lost', 'wait_for_target'),
    ('runtime_lost', 'reroute_target'),
    ('timeout', 'retry_target'),
    ('timeout', 'reroute_target'),
    ('rate_limited', 'wait_for_target'),
    ('rate_limited', 'reroute_target'),
    ('provider_failure', 'reroute_target'),
    ('provider_failure', 'wait_for_target'),
    ('network', 'reroute_target'),
    ('network', 'wait_for_target'),
    ('credential', 'request_configuration'),
    ('credential', 'wait_for_target'),
    ('configuration', 'request_configuration'),
    ('tool_failure', 'reroute_target'),
    ('tool_failure', 'request_configuration'),
    ('result_invalid', 'fresh_session'),
    ('contract_blocked', 'request_decision'),
    ('permission', 'request_decision'),
    ('capability_unavailable', 'request_decision'),
    ('target_changed', 'reroute_target'),
    ('unknown', 'retry_target'),
    ('unknown', 'reroute_target')
  );
$$;

CREATE TABLE research_target_repair (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  goal_version INTEGER NOT NULL CHECK (goal_version >= 1),
  plan_version INTEGER NOT NULL CHECK (plan_version >= 1),
  failure_class TEXT NOT NULL CHECK (btrim(failure_class) <> ''),
  failure_fingerprint TEXT NOT NULL CHECK (btrim(failure_fingerprint) <> ''),
  repair_kind TEXT NOT NULL CHECK (repair_kind IN (
    'wait_for_target', 'retry_target', 'reroute_target',
    'fresh_session', 'request_configuration', 'request_decision'
  )),
  repair_key TEXT NOT NULL CHECK (btrim(repair_key) <> ''),
  source_failure_reason TEXT NOT NULL DEFAULT '',
  target_config_fingerprint TEXT NOT NULL DEFAULT '',
  diagnostics TEXT NOT NULL DEFAULT '',
  occurrence_count INTEGER NOT NULL DEFAULT 1 CHECK (occurrence_count >= 1),
  first_attempt_id UUID REFERENCES research_task_attempt(id) ON DELETE SET NULL,
  last_attempt_id UUID REFERENCES research_task_attempt(id) ON DELETE SET NULL,
  first_observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, repair_key),
  CONSTRAINT research_target_repair_action_allowed_check
    CHECK (research_repair_action_allowed(failure_class, repair_kind)),
  CONSTRAINT research_target_repair_observation_order_check
    CHECK (last_observed_at >= first_observed_at)
);

-- Every foreign key that participates in the Agent hard-delete cascade closure
-- needs a supporting child index; research_task_attempt references agent.
CREATE INDEX research_target_repair_session_idx
  ON research_target_repair (session_id, last_observed_at DESC, id);
CREATE INDEX research_target_repair_workspace_idx
  ON research_target_repair (workspace_id);
CREATE INDEX research_target_repair_task_idx
  ON research_target_repair (task_id);
CREATE INDEX research_target_repair_first_attempt_idx
  ON research_target_repair (first_attempt_id)
  WHERE first_attempt_id IS NOT NULL;
CREATE INDEX research_target_repair_last_attempt_idx
  ON research_target_repair (last_attempt_id)
  WHERE last_attempt_id IS NOT NULL;

-- A repair record is an append-only decision plus observation counters. The
-- decided action, its canonical failure identity and the state version it was
-- decided at can never be rewritten in place: a different action requires a
-- different repair key, which is a different row.
CREATE OR REPLACE FUNCTION research_target_repair_decision_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.repair_key IS DISTINCT FROM OLD.repair_key
     OR NEW.repair_kind IS DISTINCT FROM OLD.repair_kind
     OR NEW.failure_class IS DISTINCT FROM OLD.failure_class
     OR NEW.failure_fingerprint IS DISTINCT FROM OLD.failure_fingerprint
     OR NEW.target_config_fingerprint IS DISTINCT FROM OLD.target_config_fingerprint
     OR NEW.session_id IS DISTINCT FROM OLD.session_id
     OR NEW.task_id IS DISTINCT FROM OLD.task_id
     OR NEW.goal_version IS DISTINCT FROM OLD.goal_version
     OR NEW.plan_version IS DISTINCT FROM OLD.plan_version
     OR NEW.first_attempt_id IS DISTINCT FROM OLD.first_attempt_id
     OR NEW.first_observed_at IS DISTINCT FROM OLD.first_observed_at THEN
    RAISE EXCEPTION 'research target repair decision is immutable'
      USING ERRCODE = '23514',
            CONSTRAINT = 'research_target_repair_decision_immutable_check';
  END IF;
  IF NEW.occurrence_count < OLD.occurrence_count THEN
    RAISE EXCEPTION 'research target repair occurrence count is monotonic'
      USING ERRCODE = '23514',
            CONSTRAINT = 'research_target_repair_occurrence_monotonic_check';
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS research_target_repair_decision_immutable_guard
  ON research_target_repair;
CREATE TRIGGER research_target_repair_decision_immutable_guard
BEFORE UPDATE ON research_target_repair
FOR EACH ROW EXECUTE FUNCTION research_target_repair_decision_immutable();

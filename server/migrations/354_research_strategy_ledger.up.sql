CREATE TABLE research_strategy_version (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  version_key TEXT NOT NULL CHECK (length(btrim(version_key)) BETWEEN 1 AND 128),
  previous_version_id UUID,
  config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  config_hash TEXT NOT NULL CHECK (config_hash ~ '^sha256:[0-9a-f]{64}$'),
  created_by UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, version_key),
  CONSTRAINT research_strategy_version_previous_fk
    FOREIGN KEY (workspace_id, previous_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_version_not_self_previous
    CHECK (previous_version_id IS NULL OR previous_version_id <> id)
);

CREATE TABLE research_strategy_evaluation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  strategy_version_id UUID NOT NULL,
  evaluation_run_id TEXT NOT NULL CHECK (length(btrim(evaluation_run_id)) BETWEEN 1 AND 256),
  corpus_version TEXT NOT NULL CHECK (length(btrim(corpus_version)) BETWEEN 1 AND 256),
  seed_count INTEGER NOT NULL CHECK (seed_count >= 0),
  historical_replay_count INTEGER NOT NULL CHECK (historical_replay_count >= 0),
  deterministic_invariants_passed BOOLEAN NOT NULL,
  mode_scores JSONB NOT NULL CHECK (jsonb_typeof(mode_scores) = 'object'),
  cost DOUBLE PRECISION NOT NULL CHECK (cost >= 0 AND cost < 'Infinity'::double precision),
  latency DOUBLE PRECISION NOT NULL CHECK (latency >= 0 AND latency < 'Infinity'::double precision),
  report_hash TEXT NOT NULL CHECK (report_hash ~ '^sha256:[0-9a-f]{64}$'),
  completed_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, evaluation_run_id),
  CONSTRAINT research_strategy_evaluation_version_fk
    FOREIGN KEY (workspace_id, strategy_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE research_strategy_promotion_decision (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  request_key TEXT NOT NULL CHECK (length(btrim(request_key)) BETWEEN 1 AND 256),
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^sha256:[0-9a-f]{64}$'),
  current_version_id UUID NOT NULL,
  candidate_version_id UUID NOT NULL,
  evaluation_id UUID NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('promote','reject','rollback','freeze')),
  reason TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1024),
  approved_by UUID,
  effective_version_id UUID NOT NULL,
  pointer_generation BIGINT NOT NULL CHECK (pointer_generation >= 1),
  decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, request_key),
  CONSTRAINT research_strategy_decision_current_fk
    FOREIGN KEY (workspace_id, current_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_decision_candidate_fk
    FOREIGN KEY (workspace_id, candidate_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_decision_evaluation_fk
    FOREIGN KEY (workspace_id, evaluation_id)
    REFERENCES research_strategy_evaluation(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_decision_effective_fk
    FOREIGN KEY (workspace_id, effective_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_decision_approval_guard CHECK (
    (action = 'promote' AND approved_by IS NOT NULL) OR action <> 'promote'
  ),
  CONSTRAINT research_strategy_decision_effect_guard CHECK (
    (action IN ('promote','rollback') AND effective_version_id = candidate_version_id AND current_version_id <> candidate_version_id)
    OR (action IN ('reject','freeze') AND effective_version_id = current_version_id)
  )
);

CREATE TABLE research_strategy_pointer (
  workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
  current_version_id UUID NOT NULL,
  previous_version_id UUID,
  generation BIGINT NOT NULL DEFAULT 1 CHECK (generation >= 1),
  updated_by_decision_id UUID,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_strategy_pointer_current_fk
    FOREIGN KEY (workspace_id, current_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_pointer_previous_fk
    FOREIGN KEY (workspace_id, previous_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_pointer_decision_fk
    FOREIGN KEY (workspace_id, updated_by_decision_id)
    REFERENCES research_strategy_promotion_decision(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_strategy_pointer_distinct CHECK (
    previous_version_id IS NULL OR previous_version_id <> current_version_id
  )
);

CREATE TABLE research_run_strategy_assignment (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL,
  strategy_version_id UUID NOT NULL,
  pointer_generation BIGINT NOT NULL CHECK (pointer_generation >= 1),
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, session_id),
  CONSTRAINT research_run_strategy_assignment_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_run_strategy_assignment_version_fk
    FOREIGN KEY (workspace_id, strategy_version_id)
    REFERENCES research_strategy_version(workspace_id, id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION research_validate_strategy_pointer_transition() RETURNS trigger AS $$
BEGIN
  IF NEW.generation <> OLD.generation + 1
     OR NEW.previous_version_id IS DISTINCT FROM OLD.current_version_id
     OR NEW.current_version_id IS NOT DISTINCT FROM OLD.current_version_id
     OR NEW.updated_by_decision_id IS NULL
     OR NOT EXISTS (
       SELECT 1 FROM research_strategy_promotion_decision d
       WHERE d.workspace_id = OLD.workspace_id
         AND d.id = NEW.updated_by_decision_id
         AND d.action IN ('promote','rollback')
         AND d.current_version_id = OLD.current_version_id
         AND d.effective_version_id = NEW.current_version_id
         AND d.pointer_generation = NEW.generation
     ) THEN
    RAISE EXCEPTION 'invalid Research Strategy pointer transition' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_strategy_pointer_transition_guard
BEFORE UPDATE ON research_strategy_pointer
FOR EACH ROW EXECUTE FUNCTION research_validate_strategy_pointer_transition();

CREATE OR REPLACE FUNCTION research_validate_strategy_actor() RETURNS trigger AS $$
DECLARE
  actor_id UUID;
BEGIN
  actor_id := NULLIF(
    to_jsonb(NEW) ->> CASE WHEN TG_TABLE_NAME = 'research_strategy_version' THEN 'created_by' ELSE 'approved_by' END,
    ''
  )::uuid;
  IF actor_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM member m WHERE m.workspace_id = NEW.workspace_id AND m.user_id = actor_id
  ) THEN
    RAISE EXCEPTION 'Research Strategy actor is not a workspace member' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_strategy_version_actor_guard
BEFORE INSERT ON research_strategy_version
FOR EACH ROW EXECUTE FUNCTION research_validate_strategy_actor();

CREATE TRIGGER research_strategy_decision_actor_guard
BEFORE INSERT ON research_strategy_promotion_decision
FOR EACH ROW EXECUTE FUNCTION research_validate_strategy_actor();

INSERT INTO research_strategy_version (workspace_id, version_key, config, config_hash)
SELECT id, 'research-v5-default', '{}'::jsonb,
       'sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a'
FROM workspace;

INSERT INTO research_strategy_pointer (workspace_id, current_version_id)
SELECT workspace_id, id FROM research_strategy_version WHERE version_key = 'research-v5-default';

CREATE OR REPLACE FUNCTION research_initialize_workspace_strategy() RETURNS trigger AS $$
DECLARE
  initial_version_id UUID;
BEGIN
  INSERT INTO research_strategy_version (workspace_id, version_key, config, config_hash)
  VALUES (
    NEW.id, 'research-v5-default', '{}'::jsonb,
    'sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a'
  ) RETURNING id INTO initial_version_id;
  INSERT INTO research_strategy_pointer (workspace_id, current_version_id)
  VALUES (NEW.id, initial_version_id);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER workspace_research_strategy_initialize
AFTER INSERT ON workspace
FOR EACH ROW EXECUTE FUNCTION research_initialize_workspace_strategy();

INSERT INTO research_run_strategy_assignment (workspace_id, session_id, strategy_version_id, pointer_generation, assigned_at)
SELECT s.workspace_id, s.id, p.current_version_id, p.generation, s.created_at
FROM research_session s
JOIN research_strategy_pointer p ON p.workspace_id = s.workspace_id;

CREATE OR REPLACE FUNCTION research_pin_run_strategy() RETURNS trigger AS $$
BEGIN
  INSERT INTO research_run_strategy_assignment (workspace_id, session_id, strategy_version_id, pointer_generation)
  SELECT NEW.workspace_id, NEW.id, current_version_id, generation
  FROM research_strategy_pointer WHERE workspace_id = NEW.workspace_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'Research Run requires a current Strategy version' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_session_strategy_pin
AFTER INSERT ON research_session
FOR EACH ROW EXECUTE FUNCTION research_pin_run_strategy();

CREATE OR REPLACE FUNCTION research_reject_strategy_ledger_mutation() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'research Strategy ledger rows are append-only' USING ERRCODE = '23514';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER research_strategy_version_append_only
BEFORE UPDATE OR DELETE ON research_strategy_version
FOR EACH ROW EXECUTE FUNCTION research_reject_strategy_ledger_mutation();

CREATE TRIGGER research_strategy_evaluation_append_only
BEFORE UPDATE OR DELETE ON research_strategy_evaluation
FOR EACH ROW EXECUTE FUNCTION research_reject_strategy_ledger_mutation();

CREATE TRIGGER research_strategy_decision_append_only
BEFORE UPDATE OR DELETE ON research_strategy_promotion_decision
FOR EACH ROW EXECUTE FUNCTION research_reject_strategy_ledger_mutation();

CREATE TRIGGER research_run_strategy_assignment_append_only
BEFORE UPDATE OR DELETE ON research_run_strategy_assignment
FOR EACH ROW EXECUTE FUNCTION research_reject_strategy_ledger_mutation();

CREATE INDEX research_strategy_evaluation_version_idx
  ON research_strategy_evaluation(workspace_id, strategy_version_id, completed_at DESC);
CREATE INDEX research_strategy_decision_version_idx
  ON research_strategy_promotion_decision(workspace_id, candidate_version_id, decided_at DESC);

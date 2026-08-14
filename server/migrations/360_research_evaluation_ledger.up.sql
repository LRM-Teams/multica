-- Chapter M: durable, tenant-scoped evaluation runs and immutable evidence.

CREATE TABLE research_evaluation_run (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  client_request_id TEXT NOT NULL CHECK (client_request_id <> '' AND octet_length(client_request_id) <= 512),
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^sha256:[0-9a-f]{64}$'),
  corpus_version TEXT NOT NULL CHECK (corpus_version <> '' AND octet_length(corpus_version) <= 512),
  strategy_version TEXT NOT NULL CHECK (strategy_version <> '' AND octet_length(strategy_version) <= 512),
  baseline_strategy_version TEXT NOT NULL DEFAULT '' CHECK (octet_length(baseline_strategy_version) <= 512),
  seeds BIGINT[] NOT NULL CHECK (cardinality(seeds) > 0),
  environment JSONB NOT NULL CHECK (jsonb_typeof(environment) = 'object'),
  environment_hash TEXT NOT NULL CHECK (environment_hash ~ '^sha256:[0-9a-f]{64}$'),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','cancelled')),
  failure_reason TEXT NOT NULL DEFAULT '' CHECK (octet_length(failure_reason) <= 32768),
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, client_request_id),
  CHECK ((status = 'pending' AND started_at IS NULL AND completed_at IS NULL) OR
         (status = 'running' AND started_at IS NOT NULL AND completed_at IS NULL) OR
         (status IN ('completed','failed','cancelled') AND completed_at IS NOT NULL)),
  CHECK ((status = 'failed') = (failure_reason <> ''))
);

CREATE TABLE research_evaluation_trial (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  evaluation_run_id UUID NOT NULL,
  task_id TEXT NOT NULL CHECK (task_id <> '' AND octet_length(task_id) <= 512),
  seed BIGINT NOT NULL,
  execution_error TEXT NOT NULL DEFAULT '' CHECK (octet_length(execution_error) <= 32768),
  score DOUBLE PRECISION NOT NULL CHECK (score >= 0 AND score <= 1),
  passed BOOLEAN NOT NULL,
  artifact JSONB CHECK (artifact IS NULL OR jsonb_typeof(artifact) = 'object'),
  artifact_hash TEXT NOT NULL DEFAULT '' CHECK (artifact_hash = '' OR artifact_hash ~ '^sha256:[0-9a-f]{64}$'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, evaluation_run_id, id),
  UNIQUE (workspace_id, evaluation_run_id, task_id, seed),
  FOREIGN KEY (workspace_id, evaluation_run_id)
    REFERENCES research_evaluation_run(workspace_id, id) ON DELETE CASCADE,
  CHECK ((execution_error = '') = (artifact IS NOT NULL) AND
         (artifact IS NOT NULL) = (artifact_hash <> ''))
);

CREATE TABLE research_evaluation_grade (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  evaluation_run_id UUID NOT NULL,
  trial_id UUID NOT NULL,
  grader_name TEXT NOT NULL CHECK (grader_name <> '' AND octet_length(grader_name) <= 512),
  score DOUBLE PRECISION NOT NULL CHECK (score >= 0 AND score <= 1),
  passed BOOLEAN NOT NULL,
  metrics JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metrics) = 'object'),
  findings JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(findings) = 'array'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, evaluation_run_id, trial_id, grader_name),
  FOREIGN KEY (workspace_id, evaluation_run_id, trial_id)
    REFERENCES research_evaluation_trial(workspace_id, evaluation_run_id, id) ON DELETE CASCADE
);

CREATE TABLE research_evaluation_report (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  evaluation_run_id UUID NOT NULL,
  report_hash TEXT NOT NULL CHECK (report_hash ~ '^sha256:[0-9a-f]{64}$'),
  report JSONB NOT NULL CHECK (jsonb_typeof(report) = 'object'),
  comparison JSONB CHECK (comparison IS NULL OR jsonb_typeof(comparison) = 'object'),
  passed BOOLEAN NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, evaluation_run_id),
  FOREIGN KEY (workspace_id, evaluation_run_id)
    REFERENCES research_evaluation_run(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX research_evaluation_run_workspace_status_idx
  ON research_evaluation_run(workspace_id, status, created_at DESC);
CREATE INDEX research_evaluation_trial_run_idx
  ON research_evaluation_trial(workspace_id, evaluation_run_id, task_id, seed);

CREATE OR REPLACE FUNCTION research_evaluation_immutable_row_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND NOT EXISTS (
    SELECT 1 FROM research_evaluation_run run
    WHERE run.workspace_id = OLD.workspace_id AND run.id = OLD.evaluation_run_id
  ) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'research evaluation evidence is immutable'
    USING ERRCODE = '55000', CONSTRAINT = TG_NAME;
END;
$$;

CREATE TRIGGER research_evaluation_trial_immutable
BEFORE UPDATE OR DELETE ON research_evaluation_trial
FOR EACH ROW EXECUTE FUNCTION research_evaluation_immutable_row_fn();
CREATE TRIGGER research_evaluation_grade_immutable
BEFORE UPDATE OR DELETE ON research_evaluation_grade
FOR EACH ROW EXECUTE FUNCTION research_evaluation_immutable_row_fn();
CREATE TRIGGER research_evaluation_report_immutable
BEFORE UPDATE OR DELETE ON research_evaluation_report
FOR EACH ROW EXECUTE FUNCTION research_evaluation_immutable_row_fn();

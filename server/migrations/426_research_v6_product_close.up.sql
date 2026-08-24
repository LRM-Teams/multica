-- V6 product close: release control, monitors, production episodes, and
-- the remaining Source Ingestion kinds. Operational tables are workspace
-- scoped; they are not Research Run canonical state.

CREATE TABLE research_v6_release_control (
  workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
  create_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  maintenance_reason TEXT NOT NULL DEFAULT '',
  paused_run_count INTEGER NOT NULL DEFAULT 0 CHECK (paused_run_count >= 0),
  updated_by UUID,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE research_monitor (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL,
  question_id UUID,
  search_plan_id UUID,
  search_plan_version INTEGER NOT NULL DEFAULT 1 CHECK (search_plan_version >= 1),
  baseline_report_id UUID,
  status TEXT NOT NULL CHECK (status IN ('active','paused','cancelled','blocked','budget_exhausted')),
  interval_seconds INTEGER NOT NULL CHECK (interval_seconds >= 60 AND interval_seconds <= 2592000),
  next_run_at TIMESTAMPTZ NOT NULL,
  materiality_threshold DOUBLE PRECISION NOT NULL CHECK (materiality_threshold >= 0 AND materiality_threshold <= 1),
  remaining_budget DOUBLE PRECISION NOT NULL DEFAULT 1 CHECK (remaining_budget >= 0 AND remaining_budget < 'Infinity'::double precision),
  credentials_valid BOOLEAN NOT NULL DEFAULT TRUE,
  source_reachable BOOLEAN NOT NULL DEFAULT TRUE,
  last_cycle_status TEXT NOT NULL DEFAULT '',
  last_cycle_reason TEXT NOT NULL DEFAULT '',
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  CONSTRAINT research_monitor_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX research_monitor_due_idx
  ON research_monitor (status, next_run_at)
  WHERE status = 'active';

CREATE TABLE research_monitor_cycle (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  monitor_id UUID NOT NULL,
  cycle_key TEXT NOT NULL CHECK (length(btrim(cycle_key)) BETWEEN 1 AND 128),
  status TEXT NOT NULL,
  reason TEXT NOT NULL,
  content_difference DOUBLE PRECISION,
  decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, monitor_id, cycle_key),
  CONSTRAINT research_monitor_cycle_fk
    FOREIGN KEY (workspace_id, monitor_id)
    REFERENCES research_monitor(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE research_production_episode (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL,
  strategy_version TEXT NOT NULL CHECK (length(btrim(strategy_version)) BETWEEN 1 AND 128),
  observed_at TIMESTAMPTZ NOT NULL,
  quality_score DOUBLE PRECISION NOT NULL CHECK (quality_score >= 0 AND quality_score <= 1),
  quality_passed BOOLEAN NOT NULL,
  quality_signal TEXT NOT NULL CHECK (quality_signal IN ('user_confirmed_delivery')),
  cost_units DOUBLE PRECISION NOT NULL CHECK (cost_units >= 0 AND cost_units < 'Infinity'::double precision),
  budget_units DOUBLE PRECISION NOT NULL CHECK (budget_units > 0 AND budget_units < 'Infinity'::double precision),
  PRIMARY KEY (workspace_id, session_id),
  CONSTRAINT research_production_episode_session_fk
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE research_production_window_report (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  strategy_version TEXT NOT NULL CHECK (length(btrim(strategy_version)) BETWEEN 1 AND 128),
  sufficient_data BOOLEAN NOT NULL,
  within_bounds BOOLEAN NOT NULL,
  report JSONB NOT NULL CHECK (jsonb_typeof(report) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX research_production_window_report_workspace_idx
  ON research_production_window_report (workspace_id, created_at DESC);

ALTER TABLE research_source_snapshot
  ADD COLUMN IF NOT EXISTS origin_user_id UUID,
  ADD COLUMN IF NOT EXISTS origin_attachment_id UUID,
  ADD COLUMN IF NOT EXISTS origin_workspace_artifact_id UUID,
  ADD COLUMN IF NOT EXISTS origin_adapter TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS origin_dataset_id TEXT NOT NULL DEFAULT '';

ALTER TABLE research_source_snapshot
  DROP CONSTRAINT IF EXISTS research_source_snapshot_ingestion_kind_check;
ALTER TABLE research_source_snapshot
  ADD CONSTRAINT research_source_snapshot_ingestion_kind_check
  CHECK (ingestion_kind IN (
    'agent_direct_evidence',
    'screened_retrieval',
    'user_attachment',
    'workspace_artifact',
    'api_dataset'
  ));

ALTER TABLE research_source_snapshot
  DROP CONSTRAINT IF EXISTS research_source_snapshot_ingestion_lineage_check;
ALTER TABLE research_source_snapshot
  ADD CONSTRAINT research_source_snapshot_ingestion_lineage_check
  CHECK (
    (ingestion_kind = 'screened_retrieval' AND screening_decision_id IS NOT NULL
      AND origin_user_id IS NULL AND origin_attachment_id IS NULL
      AND origin_workspace_artifact_id IS NULL AND origin_adapter = '' AND origin_dataset_id = '')
    OR (ingestion_kind = 'agent_direct_evidence' AND screening_decision_id IS NULL
      AND origin_user_id IS NULL AND origin_attachment_id IS NULL
      AND origin_workspace_artifact_id IS NULL AND origin_adapter = '' AND origin_dataset_id = '')
    OR (ingestion_kind = 'user_attachment' AND screening_decision_id IS NULL
      AND origin_user_id IS NOT NULL AND origin_attachment_id IS NOT NULL
      AND origin_workspace_artifact_id IS NULL AND origin_adapter = '' AND origin_dataset_id = '')
    OR (ingestion_kind = 'workspace_artifact' AND screening_decision_id IS NULL
      AND origin_workspace_artifact_id IS NOT NULL AND origin_user_id IS NULL
      AND origin_attachment_id IS NULL AND origin_adapter = '' AND origin_dataset_id = '')
    OR (ingestion_kind = 'api_dataset' AND screening_decision_id IS NULL
      AND origin_adapter <> '' AND origin_dataset_id <> '' AND origin_user_id IS NULL
      AND origin_attachment_id IS NULL AND origin_workspace_artifact_id IS NULL)
  );

CREATE OR REPLACE FUNCTION research_validate_source_snapshot_screening_lineage()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  candidate research_source_candidate%ROWTYPE;
BEGIN
  IF NEW.ingestion_kind <> 'screened_retrieval' THEN
    RETURN NEW;
  END IF;

  SELECT c.* INTO candidate
  FROM research_screening_decision d
  JOIN research_source_candidate c
    ON (c.workspace_id, c.session_id, c.id) =
       (d.workspace_id, d.session_id, d.source_candidate_id)
  WHERE d.workspace_id = NEW.workspace_id
    AND d.session_id = NEW.session_id
    AND d.id = NEW.screening_decision_id
    AND d.disposition = 'accepted';

  IF NOT FOUND OR candidate.canonical_url <> NEW.canonical_url OR
     (candidate.content_hash <> '' AND candidate.content_hash <> ('sha256:' || NEW.content_hash)) THEN
    RAISE EXCEPTION 'screened Research source must match an accepted Screening Decision'
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

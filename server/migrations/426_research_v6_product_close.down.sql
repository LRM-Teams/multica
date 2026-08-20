CREATE OR REPLACE FUNCTION research_validate_source_snapshot_screening_lineage()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  candidate research_source_candidate%ROWTYPE;
BEGIN
  IF NEW.ingestion_kind = 'agent_direct_evidence' THEN
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

ALTER TABLE research_source_snapshot
  DROP CONSTRAINT IF EXISTS research_source_snapshot_ingestion_lineage_check;
ALTER TABLE research_source_snapshot
  DROP CONSTRAINT IF EXISTS research_source_snapshot_ingestion_kind_check;

-- New product-close kinds have no pre-426 equivalent besides direct evidence.
-- Remap before narrowing so rollback does not fail on real rows.
UPDATE research_source_snapshot
SET ingestion_kind = 'agent_direct_evidence'
WHERE ingestion_kind IN ('user_attachment', 'workspace_artifact', 'api_dataset');

ALTER TABLE research_source_snapshot
  ADD CONSTRAINT research_source_snapshot_ingestion_kind_check
  CHECK (ingestion_kind IN ('agent_direct_evidence', 'screened_retrieval'));
ALTER TABLE research_source_snapshot
  ADD CONSTRAINT research_source_snapshot_ingestion_lineage_check
  CHECK ((ingestion_kind = 'agent_direct_evidence' AND screening_decision_id IS NULL) OR
         (ingestion_kind = 'screened_retrieval' AND screening_decision_id IS NOT NULL));

ALTER TABLE research_source_snapshot
  DROP COLUMN IF EXISTS origin_user_id,
  DROP COLUMN IF EXISTS origin_attachment_id,
  DROP COLUMN IF EXISTS origin_workspace_artifact_id,
  DROP COLUMN IF EXISTS origin_adapter,
  DROP COLUMN IF EXISTS origin_dataset_id;

DROP TABLE IF EXISTS research_production_window_report;
DROP TABLE IF EXISTS research_production_episode;
DROP TABLE IF EXISTS research_monitor_cycle;
DROP TABLE IF EXISTS research_monitor;
DROP TABLE IF EXISTS research_v6_release_control;

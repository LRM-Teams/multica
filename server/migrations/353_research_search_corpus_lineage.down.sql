DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM research_search_plan LIMIT 1)
     OR EXISTS (SELECT 1 FROM research_query_execution LIMIT 1)
     OR EXISTS (SELECT 1 FROM research_source_candidate LIMIT 1)
     OR EXISTS (SELECT 1 FROM research_screening_decision LIMIT 1) THEN
    RAISE EXCEPTION 'cannot downgrade Research Search/Corpus lineage while canonical rows exist'
      USING ERRCODE = '55000';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS research_source_snapshot_screening_lineage_guard ON research_source_snapshot;
DROP FUNCTION IF EXISTS research_validate_source_snapshot_screening_lineage();

ALTER TABLE research_source_snapshot
  DROP CONSTRAINT IF EXISTS research_source_snapshot_ingestion_lineage_check,
  DROP CONSTRAINT IF EXISTS research_source_snapshot_screening_decision_scoped_fkey,
  DROP COLUMN IF EXISTS screening_decision_id,
  DROP COLUMN IF EXISTS ingestion_kind;

DROP TRIGGER IF EXISTS research_screening_decision_append_only_guard ON research_screening_decision;
DROP TRIGGER IF EXISTS research_source_candidate_append_only_guard ON research_source_candidate;
DROP TRIGGER IF EXISTS research_query_execution_append_only_guard ON research_query_execution;
DROP TRIGGER IF EXISTS research_search_plan_append_only_guard ON research_search_plan;
DROP FUNCTION IF EXISTS research_reject_search_lineage_mutation();

DROP TRIGGER IF EXISTS research_search_lineage_passport_delete_guard ON research_artifact_passport;
DROP FUNCTION IF EXISTS research_search_lineage_passport_delete_guard_fn();

DROP TABLE IF EXISTS research_screening_decision;
DROP TABLE IF EXISTS research_source_candidate;
DROP TABLE IF EXISTS research_query_execution;
DROP TABLE IF EXISTS research_search_plan;
DROP INDEX IF EXISTS research_task_attempt_search_lineage_uidx;

CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT kind IN (
    'run_session', 'contract_revision', 'method_decision', 'question', 'task',
    'attempt', 'result_artifact', 'legacy_source', 'source_snapshot',
    'observation', 'claim', 'evidence_link', 'report_revision',
    'evaluation_decision', 'stage_evaluation', 'research_message',
    'product_round_decision', 'context_manifest', 'run_event', 'graph_node',
    'graph_edge', 'hypothesis', 'branch', 'insight', 'inquiry_edge',
    'integration_round', 'integration_contribution', 'insight_derivation',
    'dispute', 'dispute_position', 'deliberation', 'deliberation_turn'
  );
$$;

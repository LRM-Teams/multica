DROP TABLE IF EXISTS research_deliberation_turn;
DROP TABLE IF EXISTS research_deliberation;
DROP TABLE IF EXISTS research_dispute_position;
DROP TRIGGER IF EXISTS research_dispute_subject_guard ON research_dispute;
DROP FUNCTION IF EXISTS research_validate_dispute_subject();
DROP TABLE IF EXISTS research_dispute;
DROP TRIGGER IF EXISTS research_insight_derivation_input_guard ON research_insight_derivation;
DROP FUNCTION IF EXISTS research_validate_insight_derivation_input();
DROP TABLE IF EXISTS research_insight_derivation;
DROP TABLE IF EXISTS research_integration_contribution;
DROP TABLE IF EXISTS research_integration_round;

CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT kind IN (
    'run_session', 'contract_revision', 'method_decision', 'question', 'task',
    'attempt', 'result_artifact', 'legacy_source', 'source_snapshot',
    'observation', 'claim', 'evidence_link', 'report_revision',
    'evaluation_decision', 'stage_evaluation', 'research_message',
    'product_round_decision', 'context_manifest', 'run_event', 'graph_node',
    'graph_edge', 'hypothesis', 'branch', 'insight', 'inquiry_edge'
  );
$$;

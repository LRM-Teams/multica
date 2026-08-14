DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_delete_guard ON research_director_identity;
DROP TRIGGER IF EXISTS research_director_identity_artifact_passport_guard ON research_director_identity;
DROP TRIGGER IF EXISTS research_director_identity_passport_class_guard ON research_artifact_passport;
DROP TRIGGER IF EXISTS research_artifact_passport_class_guard ON research_artifact_passport;
CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
WHEN (NEW.entity_kind NOT IN ('hypothesis','branch','insight','inquiry_edge'))
EXECUTE FUNCTION research_artifact_passport_class_guard_fn();
DROP FUNCTION IF EXISTS research_director_identity_passport_class_guard_fn();
DROP TABLE IF EXISTS research_director_identity;
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
    'dispute', 'dispute_position', 'deliberation', 'deliberation_turn',
    'search_plan', 'query_execution', 'source_candidate', 'screening_decision'
  );
$$;

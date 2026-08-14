DROP TABLE IF EXISTS research_v6_artifact_class;
CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT kind IN (
  'run_session','contract_revision','method_decision','question','task','attempt','result_artifact','legacy_source',
  'source_snapshot','observation','claim','evidence_link','report_revision','evaluation_decision','stage_evaluation',
  'research_message','product_round_decision','context_manifest','run_event','graph_node','graph_edge','hypothesis',
  'branch','insight','inquiry_edge','integration_round','integration_contribution','insight_derivation','dispute',
  'dispute_position','deliberation','deliberation_turn','search_plan','query_execution','source_candidate',
  'screening_decision','research_director_identity'
 );
$$;

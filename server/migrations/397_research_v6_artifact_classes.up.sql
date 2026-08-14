CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
 SELECT kind IN (
  'run_session','contract_revision','method_decision','question','task','attempt','result_artifact','legacy_source',
  'source_snapshot','observation','claim','evidence_link','report_revision','evaluation_decision','stage_evaluation',
  'research_message','product_round_decision','context_manifest','run_event','graph_node','graph_edge','hypothesis',
  'branch','insight','inquiry_edge','integration_round','integration_contribution','insight_derivation','dispute',
  'dispute_position','deliberation','deliberation_turn','search_plan','query_execution','source_candidate',
  'screening_decision','research_director_identity','v6_director_action','v6_steering_assessment','v6_result_node',
  'v6_insight_version','v6_match_decision','v6_discussion','v6_discussion_turn','v6_report_review'
 );
$$;

CREATE TABLE research_v6_artifact_class (
 entity_kind TEXT PRIMARY KEY CHECK(research_artifact_entity_kind_allowed(entity_kind)),
 schema_id TEXT NOT NULL, default_access TEXT NOT NULL CHECK(default_access IN ('catalog','brief','full','frozen_source','control')),
 append_only BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO research_v6_artifact_class(entity_kind,schema_id,default_access) VALUES
 ('v6_director_action','director_action_proposal','control'),('v6_steering_assessment','steering_assessment_v6','control'),
 ('v6_result_node','atomic_result_submission','full'),('v6_insight_version','insight_version_v6','full'),
 ('v6_match_decision','match_decision_v6','brief'),('v6_discussion','discussion_v6','brief'),
 ('v6_discussion_turn','discussion_turn_submission','full'),('v6_report_review','report_review_v6','control');


DROP TRIGGER IF EXISTS research_claim_method_standard_diagnostic_refresh ON research_claim;
DROP FUNCTION IF EXISTS research_artifact_claim_method_diagnostic_refresh_fn();
DROP FUNCTION IF EXISTS research_artifact_scan_research_claim_method_diagnostics(UUID,UUID,UUID);
DELETE FROM research_artifact_migration_diagnostic
WHERE owner_kind='claim' AND field_path='/evidence_standard_key';

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT parser IN (
    'research_message_match_decision','research_decision_inputs',
    'research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload',
    'research_task_remediation_acceptance_criteria',
    'research_decision_evaluation_local_references'
  );
$$;

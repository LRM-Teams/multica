DROP TRIGGER IF EXISTS research_message_relationship_diagnostic_refresh ON research_message;
DROP FUNCTION IF EXISTS research_artifact_message_diagnostic_refresh_fn();
DROP FUNCTION IF EXISTS research_artifact_scan_research_message_sender_diagnostics(UUID,UUID,UUID);
DROP TRIGGER IF EXISTS research_message_sender_principal_guard ON research_message;
DROP FUNCTION IF EXISTS research_message_sender_principal_guard_fn();

ALTER TABLE research_message
  DROP CONSTRAINT IF EXISTS research_message_run_event_scoped_fkey,
  DROP CONSTRAINT IF EXISTS research_message_target_agent_scoped_fkey;

ALTER TABLE research_message
  ADD CONSTRAINT research_message_target_agent_id_fkey
    FOREIGN KEY (target_agent_id) REFERENCES agent(id) ON DELETE SET NULL,
  ADD CONSTRAINT research_message_run_event_id_fkey
    FOREIGN KEY (run_event_id) REFERENCES research_run_event(id) ON DELETE SET NULL;

DELETE FROM research_artifact_migration_diagnostic
WHERE owner_kind='research_message' AND field_path='/sender_id';

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT parser IN (
    'research_message_match_decision','research_decision_inputs',
    'research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload',
    'research_task_remediation_acceptance_criteria',
    'research_decision_evaluation_local_references',
    'research_claim_method_evidence_standard'
  );
$$;

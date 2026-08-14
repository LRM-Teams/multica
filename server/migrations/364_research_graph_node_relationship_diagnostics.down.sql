DROP TRIGGER IF EXISTS research_graph_node_relationship_diagnostic_guard ON research_graph_node;
DROP FUNCTION IF EXISTS research_artifact_graph_node_diagnostic_trigger_fn();
DROP FUNCTION IF EXISTS research_artifact_scan_research_graph_node_migration_diagnostics(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_diagnose_graph_node_reference(UUID,UUID,UUID,TEXT,TEXT,TEXT);

ALTER TABLE research_graph_node
  DROP CONSTRAINT IF EXISTS research_graph_node_run_event_scoped_fkey;

DELETE FROM research_artifact_migration_diagnostic WHERE owner_kind='graph_node';

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN (
    'research_message_match_decision',
    'research_decision_inputs',
    'research_report_structured',
    'research_run_event_payload'
  );
$$;

-- Roll back Chapter D completion (328).

ALTER TABLE research_graph_edge DROP CONSTRAINT IF EXISTS research_graph_edge_to_node_scoped_fkey;
ALTER TABLE research_graph_edge DROP CONSTRAINT IF EXISTS research_graph_edge_from_node_scoped_fkey;
ALTER TABLE research_graph_edge
  ADD CONSTRAINT research_graph_edge_from_node_id_fkey
  FOREIGN KEY (from_node_id) REFERENCES research_graph_node(id) ON DELETE CASCADE;
ALTER TABLE research_graph_edge
  ADD CONSTRAINT research_graph_edge_to_node_id_fkey
  FOREIGN KEY (to_node_id) REFERENCES research_graph_node(id) ON DELETE CASCADE;

ALTER TABLE research_report_claim DROP CONSTRAINT IF EXISTS research_report_claim_claim_scoped_fkey;
ALTER TABLE research_report_claim DROP CONSTRAINT IF EXISTS research_report_claim_report_scoped_fkey;
ALTER TABLE research_report_claim DROP CONSTRAINT IF EXISTS research_report_claim_pkey;
ALTER TABLE research_report_claim
  ADD CONSTRAINT research_report_claim_pkey
  PRIMARY KEY (report_id, claim_id, section_id);
ALTER TABLE research_report_claim
  ADD CONSTRAINT research_report_claim_report_id_fkey
  FOREIGN KEY (report_id) REFERENCES research_report(id) ON DELETE CASCADE;
ALTER TABLE research_report_claim
  ADD CONSTRAINT research_report_claim_claim_id_fkey
  FOREIGN KEY (claim_id) REFERENCES research_claim(id) ON DELETE CASCADE;
ALTER TABLE research_report_claim DROP COLUMN IF EXISTS workspace_id;
ALTER TABLE research_report_claim DROP COLUMN IF EXISTS session_id;

DROP FUNCTION IF EXISTS research_artifact_scan_research_run_event_migration_diagnostics(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_scan_research_decision_migration_diagnostics(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_report_reference(UUID, UUID, TEXT, UUID, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_attempt_reference(UUID, UUID, TEXT, UUID, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_task_reference(UUID, UUID, TEXT, UUID, TEXT, TEXT);

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN ('research_message_match_decision');
$$;

ALTER TABLE research_session DROP COLUMN IF EXISTS artifact_passport_enabled;

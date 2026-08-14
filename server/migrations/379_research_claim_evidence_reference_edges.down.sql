-- Input-reference history is append-only; preserve materialized lineage.
DROP FUNCTION IF EXISTS research_artifact_materialize_evidence_link_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_materialize_claim_references(UUID,UUID,UUID);
DROP FUNCTION IF EXISTS research_artifact_insert_claim_evidence_reference(UUID,UUID,TEXT,UUID,UUID,TEXT,TEXT,TEXT,TEXT);

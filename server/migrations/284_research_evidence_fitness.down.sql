ALTER TABLE research_claim_evidence
  DROP COLUMN IF EXISTS method_fit,
  DROP COLUMN IF EXISTS directness;

DROP INDEX IF EXISTS research_claim_evidence_standard_idx;

ALTER TABLE research_claim
  DROP COLUMN IF EXISTS evidence_standard_key;

DROP INDEX IF EXISTS research_source_snapshot_evidence_traits_idx;

ALTER TABLE research_source_snapshot
  DROP COLUMN IF EXISTS evidence_traits;

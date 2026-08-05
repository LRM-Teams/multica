ALTER TABLE research_source_snapshot
  ADD COLUMN evidence_traits TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

CREATE INDEX research_source_snapshot_evidence_traits_idx
  ON research_source_snapshot USING GIN (evidence_traits);

ALTER TABLE research_claim
  ADD COLUMN evidence_standard_key TEXT NOT NULL DEFAULT '';

CREATE INDEX research_claim_evidence_standard_idx
  ON research_claim (session_id, goal_version, plan_version, evidence_standard_key);

ALTER TABLE research_claim_evidence
  ADD COLUMN directness DOUBLE PRECISION NOT NULL DEFAULT 0.5
    CHECK (directness >= 0 AND directness <= 1),
  ADD COLUMN method_fit DOUBLE PRECISION NOT NULL DEFAULT 0.5
    CHECK (method_fit >= 0 AND method_fit <= 1);

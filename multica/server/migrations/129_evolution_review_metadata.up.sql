ALTER TABLE evolution_unit_submission
  ADD COLUMN review_decision TEXT NOT NULL DEFAULT ''
    CHECK (review_decision IN ('', 'promote', 'needs_review', 'reject')),
  ADD COLUMN review_confidence DOUBLE PRECISION,
  ADD COLUMN review_risk_level TEXT NOT NULL DEFAULT ''
    CHECK (review_risk_level IN ('', 'low', 'medium', 'high')),
  ADD COLUMN review_reason TEXT NOT NULL DEFAULT '',
  ADD COLUMN review_metadata JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN reviewed_at TIMESTAMPTZ;

ALTER TABLE evolution_unit_submission
  DROP CONSTRAINT evolution_unit_submission_status_check;

ALTER TABLE evolution_unit_submission
  ADD CONSTRAINT evolution_unit_submission_status_check
  CHECK (status IN ('candidate', 'needs_review', 'rejected', 'clustered', 'promoted', 'archived'));

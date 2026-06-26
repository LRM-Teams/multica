UPDATE evolution_unit_submission
SET status = 'candidate'
WHERE status = 'needs_review';

ALTER TABLE evolution_unit_submission
  DROP CONSTRAINT evolution_unit_submission_status_check;

ALTER TABLE evolution_unit_submission
  ADD CONSTRAINT evolution_unit_submission_status_check
  CHECK (status IN ('candidate', 'rejected', 'clustered', 'promoted', 'archived'));

ALTER TABLE evolution_unit_submission
  DROP COLUMN IF EXISTS reviewed_at,
  DROP COLUMN IF EXISTS review_metadata,
  DROP COLUMN IF EXISTS review_reason,
  DROP COLUMN IF EXISTS review_risk_level,
  DROP COLUMN IF EXISTS review_confidence,
  DROP COLUMN IF EXISTS review_decision;

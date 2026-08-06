ALTER TABLE research_session
  DROP CONSTRAINT IF EXISTS research_session_reconcile_lease_pair_check,
  DROP CONSTRAINT IF EXISTS research_session_reconcile_lease_generation_check,
  DROP COLUMN IF EXISTS reconcile_lease_generation;

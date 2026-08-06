-- A token identifies one lease claim, while the monotonically increasing
-- generation fences a paused/stale reconciler even if its process later
-- resumes. Token and expiry are one ownership fact and must change together.
ALTER TABLE research_session
  ADD COLUMN reconcile_lease_generation BIGINT NOT NULL DEFAULT 0,
  ADD CONSTRAINT research_session_reconcile_lease_generation_check
    CHECK (reconcile_lease_generation >= 0);

UPDATE research_session
SET reconcile_lease_token = NULL,
    reconcile_lease_expires_at = NULL
WHERE (reconcile_lease_token IS NULL) <> (reconcile_lease_expires_at IS NULL);

ALTER TABLE research_session
  ADD CONSTRAINT research_session_reconcile_lease_pair_check
    CHECK ((reconcile_lease_token IS NULL) = (reconcile_lease_expires_at IS NULL));

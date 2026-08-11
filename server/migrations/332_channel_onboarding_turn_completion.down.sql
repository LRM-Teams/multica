BEGIN;

DO $$
DECLARE
  completed_count BIGINT;
BEGIN
  SELECT count(*) INTO completed_count
  FROM channel_agent_onboarding
  WHERE status = 'completed';

  IF completed_count > 0 THEN
    RAISE EXCEPTION 'migration 332 down cannot proceed: % completed channel onboarding row(s) have no truthful representation in the older status contract', completed_count;
  END IF;
END $$;

ALTER TABLE channel_agent_onboarding
  DROP CONSTRAINT IF EXISTS channel_agent_onboarding_status_check;

ALTER TABLE channel_agent_onboarding
  ADD CONSTRAINT channel_agent_onboarding_status_check
  CHECK (status IN ('pending', 'claimed', 'sent', 'skipped', 'expired'));

COMMIT;

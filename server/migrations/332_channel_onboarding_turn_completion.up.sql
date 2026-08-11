BEGIN;

ALTER TABLE channel_agent_onboarding
  DROP CONSTRAINT IF EXISTS channel_agent_onboarding_status_check;

ALTER TABLE channel_agent_onboarding
  ADD CONSTRAINT channel_agent_onboarding_status_check
  CHECK (status IN ('pending', 'claimed', 'sent', 'skipped', 'completed', 'expired'));

COMMIT;

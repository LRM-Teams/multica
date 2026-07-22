BEGIN;

UPDATE channel_member
SET join_source = 'system'
WHERE join_source = 'env_dispatch';

ALTER TABLE channel_member
  DROP CONSTRAINT channel_member_join_source_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_join_source_check
  CHECK (join_source IN ('manual', 'system', 'system_general'));

DROP TRIGGER trg_maintain_channel_agent_onboarding_insert ON channel_member;
DROP TRIGGER trg_maintain_channel_agent_onboarding_delete ON channel_member;

CREATE TRIGGER trg_maintain_channel_agent_onboarding
AFTER INSERT OR DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION maintain_channel_agent_onboarding();

COMMIT;

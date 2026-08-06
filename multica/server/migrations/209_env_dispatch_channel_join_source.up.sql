BEGIN;

ALTER TABLE channel_member
  DROP CONSTRAINT channel_member_join_source_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_join_source_check
  CHECK (join_source IN ('manual', 'system', 'system_general', 'env_dispatch'));

DROP TRIGGER trg_maintain_channel_agent_onboarding ON channel_member;

CREATE TRIGGER trg_maintain_channel_agent_onboarding_insert
AFTER INSERT ON channel_member
FOR EACH ROW
WHEN (NEW.join_source <> 'env_dispatch')
EXECUTE FUNCTION maintain_channel_agent_onboarding();

CREATE TRIGGER trg_maintain_channel_agent_onboarding_delete
AFTER DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION maintain_channel_agent_onboarding();

COMMIT;

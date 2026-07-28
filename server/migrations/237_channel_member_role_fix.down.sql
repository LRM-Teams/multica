DROP TRIGGER IF EXISTS trg_channel_member_assert_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_assert_human_owner_final();
DROP TRIGGER IF EXISTS trg_channel_member_preserve_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_preserve_human_owner();

ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_role_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_role_check
  CHECK (role IN ('owner', 'manager', 'member'));

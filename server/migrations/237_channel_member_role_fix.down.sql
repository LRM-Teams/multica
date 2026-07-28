DROP TRIGGER IF EXISTS trg_workspace_member_assert_channel_owner ON member;
DROP FUNCTION IF EXISTS workspace_member_assert_channel_owner_final();
DROP TRIGGER IF EXISTS trg_channel_seed_human_owner_on_insert ON channel;
DROP FUNCTION IF EXISTS channel_seed_human_owner_on_insert();
DROP TRIGGER IF EXISTS trg_channel_assert_human_owner ON channel;
DROP FUNCTION IF EXISTS channel_assert_human_owner_final();
DROP TRIGGER IF EXISTS trg_channel_member_assert_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_assert_human_owner_final();
DROP FUNCTION IF EXISTS assert_ordinary_group_has_human_owner();
DROP TRIGGER IF EXISTS trg_channel_member_preserve_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_preserve_human_owner();

ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_role_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_role_check
  CHECK (role IN ('owner', 'manager', 'member'));

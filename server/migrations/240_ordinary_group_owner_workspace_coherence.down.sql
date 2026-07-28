-- Cannot safely restore the weaker channel_id-only owner count.
DROP TRIGGER IF EXISTS trg_workspace_member_assert_channel_owner ON member;
DROP FUNCTION IF EXISTS workspace_member_assert_channel_owner_final();

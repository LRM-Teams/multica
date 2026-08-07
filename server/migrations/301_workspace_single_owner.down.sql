DROP TRIGGER IF EXISTS member_workspace_exactly_one_owner ON member;
DROP FUNCTION IF EXISTS member_assert_exactly_one_workspace_owner();
DROP INDEX IF EXISTS member_workspace_single_owner_uidx;

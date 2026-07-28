-- Revert owner-must-be-human check to the looser 236 form.
ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_role_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_role_check
  CHECK (role IN ('owner', 'manager', 'member'));

-- Does not re-elevate DM/system owners or re-introduce agent owners.
DROP TRIGGER IF EXISTS trg_channel_member_preserve_human_owner ON channel_member;
DROP FUNCTION IF EXISTS channel_member_preserve_human_owner();

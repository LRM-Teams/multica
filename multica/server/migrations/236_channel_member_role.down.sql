DROP INDEX IF EXISTS channel_member_channel_role;
DROP INDEX IF EXISTS channel_member_one_owner;

ALTER TABLE channel_member
  DROP CONSTRAINT IF EXISTS channel_member_role_check;

ALTER TABLE channel_member
  DROP COLUMN IF EXISTS role;

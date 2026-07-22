-- LRM-233: group managers (贝克汉姆) are private — not workspace-discoverable /
-- invite-picker candidates. Existing members stay; this only flips visibility.
UPDATE agent
SET visibility = 'private', updated_at = now()
WHERE managed_role = 'group_manager'
  AND visibility <> 'private'
  AND archived_at IS NULL;

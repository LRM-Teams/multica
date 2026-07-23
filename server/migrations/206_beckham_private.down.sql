-- Reverse of 206: restore group managers to workspace visibility.
UPDATE agent
SET visibility = 'workspace', updated_at = now()
WHERE managed_role = 'group_manager'
  AND visibility = 'private'
  AND archived_at IS NULL;

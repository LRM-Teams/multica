CREATE OR REPLACE FUNCTION remove_archived_agent_group_memberships()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL THEN
    DELETE FROM channel_member membership
    USING channel ch
    WHERE membership.channel_id = ch.id
      AND membership.workspace_id = ch.workspace_id
      AND membership.member_type = 'agent'
      AND membership.member_id = NEW.id
      AND membership.workspace_id = NEW.workspace_id
      AND ch.kind = 'group';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_agent_archive_leave_groups
AFTER UPDATE OF archived_at ON agent
FOR EACH ROW
EXECUTE FUNCTION remove_archived_agent_group_memberships();

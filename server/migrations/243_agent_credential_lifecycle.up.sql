CREATE OR REPLACE FUNCTION enforce_agent_credential_active_subject()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM 1
  FROM agent AS a
  JOIN member AS m
    ON m.workspace_id = NEW.workspace_id
   AND m.user_id = NEW.user_id
  WHERE a.id = NEW.agent_id
    AND a.workspace_id = NEW.workspace_id
    AND a.archived_at IS NULL
  FOR SHARE OF a, m;

  IF NOT FOUND THEN
    RAISE EXCEPTION
      'agent credential requires an active agent and current workspace member'
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END
$$;

CREATE TRIGGER trg_agent_credential_active_subject
BEFORE INSERT ON agent_credential
FOR EACH ROW
EXECUTE FUNCTION enforce_agent_credential_active_subject();

CREATE OR REPLACE FUNCTION revoke_agent_credentials_on_archive()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  revoked_count INTEGER;
BEGIN
  IF OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL THEN
    UPDATE agent_credential
    SET revoked_at = COALESCE(revoked_at, now()),
        updated_at = now()
    WHERE agent_id = NEW.id
      AND revoked_at IS NULL;

    GET DIAGNOSTICS revoked_count = ROW_COUNT;
    IF revoked_count > 0 THEN
      INSERT INTO activity_log (
        workspace_id,
        actor_type,
        actor_id,
        action,
        details
      )
      VALUES (
        NEW.workspace_id,
        CASE WHEN NEW.archived_by IS NULL THEN 'system' ELSE 'member' END,
        NEW.archived_by,
        'agent_credential_revoked',
        jsonb_build_object(
          'agent_id', NEW.id,
          'reason', 'agent_archived',
          'revoked_count', revoked_count
        )
      );
    END IF;
  END IF;

  RETURN NEW;
END
$$;

CREATE TRIGGER trg_agent_archive_revoke_credentials
AFTER UPDATE OF archived_at ON agent
FOR EACH ROW
EXECUTE FUNCTION revoke_agent_credentials_on_archive();

CREATE OR REPLACE FUNCTION revoke_agent_credentials_on_member_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  revoked_count INTEGER;
BEGIN
  UPDATE agent_credential
  SET revoked_at = COALESCE(revoked_at, now()),
      updated_at = now()
  WHERE workspace_id = OLD.workspace_id
    AND user_id = OLD.user_id
    AND revoked_at IS NULL;

  GET DIAGNOSTICS revoked_count = ROW_COUNT;
  IF revoked_count > 0 THEN
    INSERT INTO activity_log (
      workspace_id,
      actor_type,
      action,
      details
    )
    VALUES (
      OLD.workspace_id,
      'system',
      'agent_credential_revoked',
      jsonb_build_object(
        'owner_user_id', OLD.user_id,
        'reason', 'owner_membership_deleted',
        'revoked_count', revoked_count
      )
    );
  END IF;

  RETURN OLD;
END
$$;

CREATE TRIGGER trg_member_delete_revoke_agent_credentials
BEFORE DELETE ON member
FOR EACH ROW
EXECUTE FUNCTION revoke_agent_credentials_on_member_delete();

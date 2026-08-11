BEGIN;

CREATE OR REPLACE FUNCTION guard_system_general_roster()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_workspace_id UUID;
  old_is_general BOOLEAN := FALSE;
  new_is_general BOOLEAN := FALSE;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    SELECT channel.system_key = 'general', channel.workspace_id
    INTO old_is_general, target_workspace_id
    FROM channel
    WHERE channel.id = OLD.channel_id;
  END IF;

  IF TG_OP <> 'DELETE' THEN
    SELECT channel.system_key = 'general', channel.workspace_id
    INTO new_is_general, target_workspace_id
    FROM channel
    WHERE channel.id = NEW.channel_id;
  END IF;

  IF TG_OP = 'UPDATE' AND (old_is_general OR new_is_general) AND (
    NEW.channel_id IS DISTINCT FROM OLD.channel_id
    OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
    OR NEW.member_type IS DISTINCT FROM OLD.member_type
    OR NEW.member_id IS DISTINCT FROM OLD.member_id
  ) THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_roster_managed';
  END IF;

  IF TG_OP = 'INSERT' AND new_is_general THEN
    IF NEW.workspace_id IS DISTINCT FROM target_workspace_id OR NOT (
      (NEW.member_type = 'user' AND EXISTS (
        SELECT 1 FROM member
        WHERE workspace_id = target_workspace_id AND user_id = NEW.member_id
      ))
      OR
      (NEW.member_type = 'agent' AND EXISTS (
        SELECT 1 FROM agent
        WHERE workspace_id = target_workspace_id
          AND id = NEW.member_id
          AND archived_at IS NULL
      ))
    ) THEN
      RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_roster_invalid_member';
    END IF;
  END IF;

  IF TG_OP = 'DELETE' AND old_is_general AND (
    (OLD.member_type = 'user' AND EXISTS (
      SELECT 1 FROM member
      WHERE workspace_id = target_workspace_id AND user_id = OLD.member_id
    ))
    OR
    (OLD.member_type = 'agent' AND EXISTS (
      SELECT 1 FROM agent
      WHERE workspace_id = target_workspace_id
        AND id = OLD.member_id
        AND archived_at IS NULL
    ))
  ) THEN
    RAISE EXCEPTION USING ERRCODE = 'P0001', MESSAGE = 'system_general_roster_managed';
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

COMMIT;

BEGIN;

-- DEPLOY ORDER: apply migration 245 before starting a server that writes
-- actor-neutral channel membership provenance. The cutover is intentionally
-- clean: there is no legacy added_by compatibility column or dual-write path.
--
-- ROLLBACK PRECONDITION: export/reconcile agent-authored membership and
-- onboarding provenance before applying the down migration. The legacy schema
-- cannot represent agent actors and the down migration is explicitly lossy.

DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding ON channel_member;
DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding_insert ON channel_member;
DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding_delete ON channel_member;

ALTER TABLE channel_member
  ADD COLUMN added_by_type TEXT,
  ADD COLUMN added_by_id UUID;

UPDATE channel_member membership
SET added_by_type = CASE
      WHEN membership.added_by IS NOT NULL THEN 'user'
      WHEN membership.member_type = 'user'
       AND membership.role = 'owner'
       AND EXISTS (
         SELECT 1
         FROM channel channel_row
         WHERE channel_row.id = membership.channel_id
           AND channel_row.workspace_id = membership.workspace_id
           AND channel_row.created_by = membership.member_id
       ) THEN 'user'
      ELSE 'system'
    END,
    added_by_id = CASE
      WHEN membership.added_by IS NOT NULL THEN membership.added_by
      WHEN membership.member_type = 'user'
       AND membership.role = 'owner'
       AND EXISTS (
         SELECT 1
         FROM channel channel_row
         WHERE channel_row.id = membership.channel_id
           AND channel_row.workspace_id = membership.workspace_id
           AND channel_row.created_by = membership.member_id
       ) THEN membership.member_id
      ELSE NULL
    END;

ALTER TABLE channel_member
  ALTER COLUMN added_by_type SET DEFAULT 'system',
  ALTER COLUMN added_by_type SET NOT NULL,
  DROP COLUMN added_by;

ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_actor_provenance_check
  CHECK (
    (added_by_type = 'system' AND added_by_id IS NULL)
    OR
    (added_by_type IN ('user', 'agent') AND added_by_id IS NOT NULL)
  );

ALTER TABLE channel_agent_onboarding
  ADD COLUMN source_actor_type TEXT;

UPDATE channel_agent_onboarding
SET source_actor_type = CASE
  WHEN source_actor_id IS NULL THEN 'system'
  ELSE 'user'
END;

ALTER TABLE channel_agent_onboarding
  ALTER COLUMN source_actor_type SET DEFAULT 'system',
  ALTER COLUMN source_actor_type SET NOT NULL,
  DROP CONSTRAINT IF EXISTS channel_agent_onboarding_source_actor_id_fkey;

ALTER TABLE channel_agent_onboarding
  ADD CONSTRAINT channel_agent_onboarding_source_actor_check
  CHECK (
    (source_actor_type = 'system' AND source_actor_id IS NULL)
    OR
    (source_actor_type IN ('user', 'agent') AND source_actor_id IS NOT NULL)
  );

-- This function is the database half of the shared actor boundary. A
-- polymorphic foreign key would not express the user-vs-agent table choice, so
-- every new/changed provenance row validates canonical shape, existence, and
-- same-workspace ownership transactionally instead.
CREATE OR REPLACE FUNCTION assert_channel_actor_exists(
  actor_workspace_id UUID,
  actor_type TEXT,
  actor_id UUID
)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
  IF actor_type = 'system' AND actor_id IS NULL THEN
    RETURN;
  END IF;

  IF actor_type = 'user'
     AND actor_id IS NOT NULL
     AND EXISTS (
       SELECT 1
       FROM member
       WHERE workspace_id = actor_workspace_id
         AND user_id = actor_id
     ) THEN
    RETURN;
  END IF;

  IF actor_type = 'agent'
     AND actor_id IS NOT NULL
     AND EXISTS (
       SELECT 1
       FROM agent
       WHERE workspace_id = actor_workspace_id
         AND id = actor_id
     ) THEN
    RETURN;
  END IF;

  RAISE EXCEPTION 'channel actor provenance must reference an existing same-workspace actor'
    USING ERRCODE = 'check_violation';
END;
$$;

CREATE OR REPLACE FUNCTION channel_member_validate_actor_provenance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM assert_channel_actor_exists(
    NEW.workspace_id,
    NEW.added_by_type,
    NEW.added_by_id
  );
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_member_validate_actor_provenance ON channel_member;
CREATE TRIGGER trg_channel_member_validate_actor_provenance
BEFORE INSERT OR UPDATE OF workspace_id, added_by_type, added_by_id
ON channel_member
FOR EACH ROW
EXECUTE FUNCTION channel_member_validate_actor_provenance();

CREATE OR REPLACE FUNCTION channel_onboarding_validate_actor_provenance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM assert_channel_actor_exists(
    NEW.workspace_id,
    NEW.source_actor_type,
    NEW.source_actor_id
  );
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_channel_onboarding_validate_actor_provenance
  ON channel_agent_onboarding;
CREATE TRIGGER trg_channel_onboarding_validate_actor_provenance
BEFORE INSERT OR UPDATE OF workspace_id, source_actor_type, source_actor_id
ON channel_agent_onboarding
FOR EACH ROW
EXECUTE FUNCTION channel_onboarding_validate_actor_provenance();

CREATE OR REPLACE FUNCTION maintain_channel_agent_onboarding()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_channel channel%ROWTYPE;
  system_message_id UUID;
  onboarding_source TEXT;
  onboarding_actor_type TEXT;
  onboarding_actor_id UUID;
  actor_handle TEXT;
  actor_display_name TEXT;
  actor_public_type TEXT;
  target_handle TEXT;
  target_display_name TEXT;
  canonical_content TEXT;
  event_params JSONB;
BEGIN
  IF TG_OP = 'INSERT' AND NEW.member_type = 'agent' THEN
    SELECT * INTO target_channel
    FROM channel
    WHERE id = NEW.channel_id;

    IF target_channel.id IS NOT NULL
       AND target_channel.kind = 'group'
       AND target_channel.archived_at IS NULL THEN
      onboarding_source := CASE
        WHEN target_channel.system_key = 'general' THEN 'system_invariant'
        ELSE NEW.join_source
      END;
      onboarding_actor_type := NEW.added_by_type;
      onboarding_actor_id := NEW.added_by_id;

      IF onboarding_source = 'system_invariant' THEN
        onboarding_actor_type := 'system';
        onboarding_actor_id := NULL;
      END IF;

      PERFORM assert_channel_actor_exists(
        NEW.workspace_id,
        onboarding_actor_type,
        onboarding_actor_id
      );

      SELECT COALESCE(NULLIF(name, ''), 'agent'),
             COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), 'Agent')
      INTO target_handle, target_display_name
      FROM agent
      WHERE workspace_id = NEW.workspace_id
        AND id = NEW.member_id;

      IF target_display_name IS NULL THEN
        RAISE EXCEPTION 'channel onboarding target must be an existing same-workspace agent'
          USING ERRCODE = 'check_violation';
      END IF;

      IF onboarding_actor_type = 'user' THEN
        SELECT COALESCE(NULLIF(u.name, ''), NULLIF(u.email, ''), 'user'),
               COALESCE(
                 NULLIF(u.display_name, ''),
                 NULLIF(u.name, ''),
                 NULLIF(u.email, ''),
                 'User'
               )
        INTO actor_handle, actor_display_name
        FROM "user" u
        JOIN member m
          ON m.user_id = u.id
         AND m.workspace_id = NEW.workspace_id
        WHERE u.id = onboarding_actor_id;
        actor_public_type := 'human';
        canonical_content := actor_display_name || ' added ' ||
          target_display_name || ' to the channel';
      ELSIF onboarding_actor_type = 'agent' THEN
        SELECT COALESCE(NULLIF(name, ''), 'agent'),
               COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), 'Agent')
        INTO actor_handle, actor_display_name
        FROM agent
        WHERE workspace_id = NEW.workspace_id
          AND id = onboarding_actor_id;
        actor_public_type := 'agent';
        canonical_content := actor_display_name || ' added ' ||
          target_display_name || ' to the channel';
      ELSE
        actor_public_type := 'system';
        actor_handle := 'system';
        actor_display_name := CASE
          WHEN onboarding_source = 'system_invariant' THEN 'Workspace'
          ELSE 'System'
        END;
        canonical_content := target_display_name || ' joined this channel';
      END IF;

      event_params := jsonb_build_object(
        'actor_id', COALESCE(onboarding_actor_id::text, ''),
        'actor_type', actor_public_type,
        'actor_handle', actor_handle,
        'actor_display_name', actor_display_name,
        'actor_name', actor_display_name,
        'target_id', NEW.member_id::text,
        'target_type', 'agent',
        'target_handle', target_handle,
        'target_display_name', target_display_name,
        'target_name', target_display_name,
        'source', onboarding_source,
        'membership_generation_id', NEW.generation_id::text
      );

      INSERT INTO channel_message (
        channel_id,
        workspace_id,
        author_type,
        author_name,
        content,
        parts,
        source,
        membership_generation_id
      )
      VALUES (
        NEW.channel_id,
        NEW.workspace_id,
        'system',
        'system',
        canonical_content,
        jsonb_build_array(jsonb_build_object(
          'type', 'system_event',
          'event', 'channel_member_added',
          'event_params', event_params
        )),
        'multica',
        NEW.generation_id
      )
      ON CONFLICT (membership_generation_id)
        WHERE membership_generation_id IS NOT NULL
      DO NOTHING
      RETURNING id INTO system_message_id;

      IF system_message_id IS NULL THEN
        SELECT id INTO system_message_id
        FROM channel_message
        WHERE membership_generation_id = NEW.generation_id;
      END IF;

      INSERT INTO channel_agent_onboarding (
        workspace_id,
        channel_id,
        agent_id,
        membership_generation_id,
        system_message_id,
        source_type,
        source_actor_type,
        source_actor_id
      )
      VALUES (
        NEW.workspace_id,
        NEW.channel_id,
        NEW.member_id,
        NEW.generation_id,
        system_message_id,
        onboarding_source,
        onboarding_actor_type,
        onboarding_actor_id
      )
      ON CONFLICT (channel_id, agent_id, membership_generation_id) DO NOTHING;
    END IF;
    RETURN NEW;
  END IF;

  IF TG_OP = 'DELETE' AND OLD.member_type = 'agent' THEN
    UPDATE agent_event_delivery delivery
    SET status = 'expired',
        last_error = 'channel onboarding membership generation removed',
        updated_at = now()
    FROM agent_inbox_event inbox,
         channel_agent_onboarding onboarding
    WHERE onboarding.channel_id = OLD.channel_id
      AND onboarding.agent_id = OLD.member_id
      AND onboarding.membership_generation_id = OLD.generation_id
      AND onboarding.status IN ('pending', 'claimed')
      AND inbox.channel_onboarding_id = onboarding.id
      AND delivery.inbox_event_id = inbox.id
      AND delivery.status IN ('leased', 'processing');

    UPDATE agent_inbox_event inbox
    SET status = 'suppressed',
        terminal_outcome = 'expired',
        retryable = FALSE,
        terminal_at = now(),
        last_error = 'channel onboarding membership generation removed',
        updated_at = now()
    FROM channel_agent_onboarding onboarding
    WHERE onboarding.channel_id = OLD.channel_id
      AND onboarding.agent_id = OLD.member_id
      AND onboarding.membership_generation_id = OLD.generation_id
      AND onboarding.status IN ('pending', 'claimed')
      AND inbox.channel_onboarding_id = onboarding.id
      AND inbox.status IN ('pending', 'draining', 'failed');

    UPDATE channel_agent_onboarding
    SET status = 'expired',
        terminal_evidence = jsonb_build_object(
          'reason',
          'membership_generation_removed'
        ),
        terminal_at = now(),
        updated_at = now()
    WHERE channel_id = OLD.channel_id
      AND agent_id = OLD.member_id
      AND membership_generation_id = OLD.generation_id
      AND status IN ('pending', 'claimed');
    RETURN OLD;
  END IF;

  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

-- Preserve migration 209's historical env-dispatch exclusion and independent
-- DELETE expiry path exactly. Copying an env-dispatch roster must never create
-- a new onboarding wake.
CREATE TRIGGER trg_maintain_channel_agent_onboarding_insert
AFTER INSERT ON channel_member
FOR EACH ROW
WHEN (NEW.join_source <> 'env_dispatch')
EXECUTE FUNCTION maintain_channel_agent_onboarding();

CREATE TRIGGER trg_maintain_channel_agent_onboarding_delete
AFTER DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION maintain_channel_agent_onboarding();

CREATE OR REPLACE FUNCTION channel_seed_human_owner_on_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.kind = 'group'
     AND NEW.system_key IS NULL
     AND NEW.created_by IS NOT NULL THEN
    IF EXISTS (
      SELECT 1
      FROM member m
      WHERE m.workspace_id = NEW.workspace_id
        AND m.user_id = NEW.created_by
    ) THEN
      INSERT INTO channel_member (
        channel_id,
        workspace_id,
        member_type,
        member_id,
        role,
        added_by_type,
        added_by_id,
        join_source
      )
      VALUES (
        NEW.id,
        NEW.workspace_id,
        'user',
        NEW.created_by,
        'owner',
        'user',
        NEW.created_by,
        'manual'
      )
      ON CONFLICT (channel_id, member_type, member_id) DO UPDATE
        SET role = CASE
          WHEN channel_member.role = 'owner' THEN channel_member.role
          ELSE 'owner'
        END;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

COMMIT;

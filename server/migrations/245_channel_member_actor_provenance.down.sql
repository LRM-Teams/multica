BEGIN;

-- LOSSY ROLLBACK: the legacy schema can represent only human users. Before
-- applying this file, export or reconcile all rows where added_by_type or
-- source_actor_type = 'agent'. Agent provenance is intentionally collapsed to
-- NULL, which the legacy model and a later re-up both interpret as system.
--
-- ROLLBACK ORDER: stop new servers first, export/reconcile agent provenance,
-- apply this migration, and only then start a legacy server.

DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding ON channel_member;
DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding_insert ON channel_member;
DROP TRIGGER IF EXISTS trg_maintain_channel_agent_onboarding_delete ON channel_member;
DROP TRIGGER IF EXISTS trg_channel_member_validate_actor_provenance ON channel_member;
DROP TRIGGER IF EXISTS trg_channel_onboarding_validate_actor_provenance
  ON channel_agent_onboarding;

ALTER TABLE channel_member
  ADD COLUMN added_by UUID;

ALTER TABLE channel_agent_onboarding
  DROP CONSTRAINT channel_agent_onboarding_source_actor_check;

UPDATE channel_member
SET added_by = CASE
  WHEN added_by_type = 'user' THEN added_by_id
  ELSE NULL
END;

UPDATE channel_agent_onboarding
SET source_actor_id = CASE
  WHEN source_actor_type = 'user' THEN source_actor_id
  ELSE NULL
END;

ALTER TABLE channel_member
  DROP CONSTRAINT channel_member_actor_provenance_check,
  DROP COLUMN added_by_type,
  DROP COLUMN added_by_id;

ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_added_by_fkey
  FOREIGN KEY (added_by) REFERENCES "user"(id) ON DELETE SET NULL;

ALTER TABLE channel_agent_onboarding
  DROP COLUMN source_actor_type;

ALTER TABLE channel_agent_onboarding
  ADD CONSTRAINT channel_agent_onboarding_source_actor_id_fkey
  FOREIGN KEY (source_actor_id) REFERENCES "user"(id) ON DELETE SET NULL;

DROP FUNCTION IF EXISTS channel_member_validate_actor_provenance();
DROP FUNCTION IF EXISTS channel_onboarding_validate_actor_provenance();
DROP FUNCTION IF EXISTS assert_channel_actor_exists(UUID, TEXT, UUID);

CREATE OR REPLACE FUNCTION maintain_channel_agent_onboarding()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_channel channel%ROWTYPE;
  system_message_id UUID;
  onboarding_source TEXT;
  actor_handle TEXT;
  actor_display_name TEXT;
  actor_type TEXT;
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

      SELECT COALESCE(NULLIF(name, ''), 'agent'),
             COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), 'Agent')
      INTO target_handle, target_display_name
      FROM agent
      WHERE id = NEW.member_id;

      IF onboarding_source = 'system_invariant' THEN
        actor_type := 'system';
        actor_handle := 'system';
        actor_display_name := 'Workspace';
        canonical_content := target_display_name || ' joined this channel';
      ELSIF NEW.added_by IS NOT NULL THEN
        SELECT COALESCE(NULLIF(name, ''), NULLIF(email, ''), 'user'),
               COALESCE(
                 NULLIF(display_name, ''),
                 NULLIF(name, ''),
                 NULLIF(email, ''),
                 'User'
               )
        INTO actor_handle, actor_display_name
        FROM "user"
        WHERE id = NEW.added_by;
        actor_type := 'human';
        canonical_content := actor_display_name || ' added ' ||
          target_display_name || ' to the channel';
      ELSE
        actor_type := 'system';
        actor_handle := 'system';
        actor_display_name := 'System';
        canonical_content := target_display_name || ' joined this channel';
      END IF;

      event_params := jsonb_build_object(
        'actor_id', COALESCE(NEW.added_by::text, ''),
        'actor_type', actor_type,
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
        source_actor_id
      )
      VALUES (
        NEW.workspace_id,
        NEW.channel_id,
        NEW.member_id,
        NEW.generation_id,
        system_message_id,
        onboarding_source,
        NEW.added_by
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
        added_by,
        join_source
      )
      VALUES (
        NEW.id,
        NEW.workspace_id,
        'user',
        NEW.created_by,
        'owner',
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
